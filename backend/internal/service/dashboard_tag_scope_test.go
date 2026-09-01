package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// TestDashboardStats_TagScoping is the regression guard for the sweep's
// headline finding: every card on the dashboard was tag-scoped, but the
// time-series underneath them and the MTTA/MTTR averages were not. Two
// analysts restricted to different, non-overlapping tags received
// byte-identical trend data covering the entire tenant.
//
// The trends read materialized views keyed on tenant_id alone, so a
// restricted caller now takes a live path over the base tables instead --
// which is exactly what this asserts, by giving two roles disjoint scopes
// and requiring their numbers to differ.
func TestDashboardStats_TagScoping(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)

	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), tagSvc, repository.NewPlaybookRepository())
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(),
		service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	dash := service.NewDashboardService(pool, repository.NewDashboardRepository(), alertSvc, incSvc)
	_ = actorID

	// Three corp alerts, one finance alert -- deliberately lopsided so a
	// leak shows up as a wrong number, not just a suspicious coincidence.
	ingest := func(t *testing.T, title, tag string) {
		t.Helper()
		_, _, err := alertSvc.Ingest(t.Context(), tenantID, endpointID, domain.Alert{
			Title: title, Source: "s", Severity: domain.SeverityHigh,
			Payload: testPayload, Tags: []string{tag},
		}, nil, 0)
		require.NoError(t, err)
	}
	ingest(t, "corp one", "corp")
	ingest(t, "corp two", "corp")
	ingest(t, "corp three", "corp")
	ingest(t, "finance one", "finance")

	statsFor := func(t *testing.T, allowedTags []string) *domain.DashboardStats {
		t.Helper()
		s, err := dash.Stats(t.Context(), tenantID, repository.StatsFilter{AllowedTags: allowedTags})
		require.NoError(t, err)
		require.NotNil(t, s)
		return s
	}

	trendTotal := func(s *domain.DashboardStats) int {
		total := 0
		for _, p := range s.AlertTrend {
			total += p.AlertCount
		}
		return total
	}

	corp := statsFor(t, []string{"corp"})
	finance := statsFor(t, []string{"finance"})
	all := statsFor(t, nil)

	t.Run("the alert trend is scoped, not tenant-wide", func(t *testing.T) {
		assert.Equal(t, 3, trendTotal(corp), "corp should see only its own three alerts in the trend")
		assert.Equal(t, 1, trendTotal(finance), "finance should see only its own one")
	})

	t.Run("two disjoint scopes do not produce the same numbers", func(t *testing.T) {
		// The literal shape of the bug: before the fix both of these were
		// the tenant total, so they matched each other exactly.
		assert.NotEqual(t, trendTotal(corp), trendTotal(finance),
			"disjoint tag scopes covering different data must not report identical trends")
	})

	t.Run("the unrestricted caller keeps the materialized-view path", func(t *testing.T) {
		// Not an equality check on purpose. The unrestricted branch reads
		// mv_alert_daily_stats, which cmd/worker refreshes on a timer, so in
		// a freshly seeded test it legitimately has not caught up yet --
		// asserting "4" here would be asserting that a cache is warm.
		//
		// Worth stating plainly, because the fix introduces an asymmetry: a
		// tag-restricted caller now reads live and therefore sees FRESHER
		// trend data than an unrestricted one. That trade was accepted to
		// close the leak; the alternative was leaving the scope unapplied.
		assert.NotNil(t, all, "the unrestricted path must still work")
		assert.LessOrEqual(t, trendTotal(all), 4,
			"the view can lag behind the base tables, but must never exceed them")
	})

	t.Run("the trend agrees with the scoped card next to it", func(t *testing.T) {
		// These come from different queries (live aggregate vs live count),
		// so they can disagree -- and did. On the same fresh tenant with
		// nothing closed, every ingested alert is open.
		assert.Equal(t, corp.OpenAlerts, trendTotal(corp),
			"the trend and the open-alerts card describe the same set here")
		assert.Equal(t, finance.OpenAlerts, trendTotal(finance))
	})

	t.Run("MTTA/MTTR is scoped too", func(t *testing.T) {
		// Nothing has been acknowledged or closed in either scope, so a
		// correctly scoped average has no sample and stays nil. Before the
		// fix these read a tenant-wide materialized view and could return a
		// figure computed from rows the caller cannot see.
		assert.Nil(t, corp.AlertAvgMTTASeconds, "no acknowledged alert in scope means no average")
		assert.Nil(t, corp.AlertAvgMTTRSeconds)
		assert.Nil(t, finance.AlertAvgMTTASeconds)
		assert.Nil(t, finance.AlertAvgMTTRSeconds)
	})
}
