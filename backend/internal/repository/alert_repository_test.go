package repository_test

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func newTestAlert(tenantID uuid.UUID, severity domain.Severity, status domain.AlertStatus, tags []string) *domain.Alert {
	if tags == nil {
		tags = []string{}
	}
	return &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "wazuh",
		Severity: severity, OriginalSeverity: severity, Status: status,
		Tags: tags, Payload: json.RawMessage(`{"raw":true}`), ReceivedAt: time.Now(),
	}
}

func TestAlertRepository_InsertGet(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusOpen, []string{"phishing"})
	a.SrcIP = net.ParseIP("10.0.0.5")
	require.NoError(t, repo.Insert(t.Context(), tx, a))
	require.NotEqual(t, [16]byte{}, a.ID)

	got, err := repo.Get(t.Context(), tx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Suspicious login", got.Title)
	assert.Equal(t, domain.SeverityHigh, got.Severity)
	assert.Equal(t, []string{"phishing"}, got.Tags)
	assert.Equal(t, "10.0.0.5", got.SrcIP.String())

	t.Run("get unknown id returns nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestAlertRepository_PlaybookJoin(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	playbookRepo := repository.NewPlaybookRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	pb := &domain.Playbook{TenantID: tenantID, Title: "Phishing Response", Category: "Phishing"}
	require.NoError(t, playbookRepo.Insert(t.Context(), tx, pb))

	t.Run("no playbook_id -- both fields nil", func(t *testing.T) {
		a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
		require.NoError(t, repo.Insert(t.Context(), tx, a))
		assert.Nil(t, a.PlaybookID)
		assert.Nil(t, a.PlaybookTitle)

		got, err := repo.Get(t.Context(), tx, a.ID)
		require.NoError(t, err)
		assert.Nil(t, got.PlaybookID)
		assert.Nil(t, got.PlaybookTitle)
	})

	t.Run("playbook_id set -- Insert and Get both resolve the joined title", func(t *testing.T) {
		a := newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusOpen, nil)
		a.PlaybookID = &pb.ID
		require.NoError(t, repo.Insert(t.Context(), tx, a))
		require.NotNil(t, a.PlaybookTitle)
		assert.Equal(t, "Phishing Response", *a.PlaybookTitle)

		got, err := repo.Get(t.Context(), tx, a.ID)
		require.NoError(t, err)
		require.NotNil(t, got.PlaybookID)
		assert.Equal(t, pb.ID, *got.PlaybookID)
		require.NotNil(t, got.PlaybookTitle)
		assert.Equal(t, "Phishing Response", *got.PlaybookTitle)
	})
}

func TestAlertRepository_List_Filters(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	critOpen := newTestAlert(tenantID, domain.SeverityCritical, domain.AlertStatusOpen, []string{"phishing"})
	require.NoError(t, repo.Insert(t.Context(), tx, critOpen))
	lowInvestigating := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusInvestigating, []string{"vpn"})
	require.NoError(t, repo.Insert(t.Context(), tx, lowInvestigating))
	highEscalated := newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusEscalated, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, highEscalated))

	t.Run("no filter returns everything", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{})
		require.NoError(t, err)
		assert.Len(t, list, 3)
	})

	t.Run("filter by exact status", func(t *testing.T) {
		status := domain.AlertStatusOpen
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Status: &status})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, critOpen.ID, list[0].ID)
	})

	t.Run("filter by statuses (OR) -- follow-up-style query", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{
			Statuses: []domain.AlertStatus{domain.AlertStatusInvestigating, domain.AlertStatusEscalated},
		})
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("Status wins over Statuses when both are set", func(t *testing.T) {
		status := domain.AlertStatusOpen
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{
			Status:   &status,
			Statuses: []domain.AlertStatus{domain.AlertStatusInvestigating, domain.AlertStatusEscalated},
		})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, critOpen.ID, list[0].ID)
	})

	t.Run("filter by severity", func(t *testing.T) {
		sev := domain.SeverityCritical
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Severity: &sev})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, critOpen.ID, list[0].ID)
	})

	t.Run("filter by severities (OR) -- list page's multi-select", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{
			Severities: []domain.Severity{domain.SeverityLow, domain.SeverityHigh},
		})
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("Severity wins over Severities when both are set", func(t *testing.T) {
		sev := domain.SeverityCritical
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{
			Severity:   &sev,
			Severities: []domain.Severity{domain.SeverityLow, domain.SeverityHigh},
		})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, critOpen.ID, list[0].ID)
	})

	t.Run("filter by single tag", func(t *testing.T) {
		tag := "vpn"
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Tag: &tag})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, lowInvestigating.ID, list[0].ID)
	})

	t.Run("filter by AllowedTags -- any overlap matches", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{AllowedTags: []string{"phishing", "vpn"}})
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	// TestAlertRepository_List_Filters' other subtests each apply exactly one
	// filter -- every branch in List's where-clause builder appends its own
	// arg and computes its own "$%d" position from len(args), so a filter
	// added later in the struct could in principle clobber an earlier one's
	// positional param if that arithmetic were ever wrong. That class of bug
	// only surfaces when 2+ filters are combined in the same call, which
	// none of the single-filter subtests above would catch.
	t.Run("combined filters (severity + status + tag + source) narrow to the one alert matching all four", func(t *testing.T) {
		sev := domain.SeverityLow
		status := domain.AlertStatusInvestigating
		tag := "vpn"
		source := "wazuh"
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{
			Severity: &sev, Status: &status, Tag: &tag, Source: &source,
		})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, lowInvestigating.ID, list[0].ID)
	})

	t.Run("combined filters where one condition matches nothing returns empty, not a partial match", func(t *testing.T) {
		sev := domain.SeverityLow
		status := domain.AlertStatusOpen // lowInvestigating is "investigating", not "open"
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Severity: &sev, Status: &status})
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("filter by correlated=false excludes alerts already linked to an incident", func(t *testing.T) {
		correlated := false
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Correlated: &correlated})
		require.NoError(t, err)
		assert.Len(t, list, 3, "none of these alerts are linked to an incident yet")
	})

	t.Run("limit and offset paginate", func(t *testing.T) {
		page1, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Limit: 2, Offset: 0})
		require.NoError(t, err)
		assert.Len(t, page1, 2)

		page2, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Limit: 2, Offset: 2})
		require.NoError(t, err)
		assert.Len(t, page2, 1)
	})
}

func TestAlertRepository_List_FilterByQ(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	ransomware := newTestAlert(tenantID, domain.SeverityCritical, domain.AlertStatusOpen, nil)
	ransomware.Title = "Ransomware encryption detected on file server"
	require.NoError(t, repo.Insert(t.Context(), tx, ransomware))

	bastionAsset := "bastion-01"
	bruteforce := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	bruteforce.Title = "Repeated failed SSH logins"
	bruteforce.Asset = &bastionAsset
	require.NoError(t, repo.Insert(t.Context(), tx, bruteforce))

	t.Run("matches a word in the title", func(t *testing.T) {
		q := "ransomware"
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Q: &q})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, ransomware.ID, list[0].ID)
	})

	t.Run("matches a word in the asset field, not just title", func(t *testing.T) {
		q := "bastion"
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Q: &q})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, bruteforce.ID, list[0].ID)
	})

	t.Run("no matches returns empty, not an error", func(t *testing.T) {
		q := "nonexistentkeyword"
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{Q: &q})
		require.NoError(t, err)
		assert.Empty(t, list)
	})
}

// TestAlertRepository_Count is the regression test for real page-number
// pagination: Count must apply the same filters as List but ignore
// Limit/Offset entirely, so a caller can compute total pages independent of
// which page it's currently viewing.
func TestAlertRepository_Count(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.Insert(t.Context(), tx, newTestAlert(tenantID, domain.SeverityCritical, domain.AlertStatusOpen, []string{"phishing"})))
	require.NoError(t, repo.Insert(t.Context(), tx, newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusInvestigating, []string{"vpn"})))
	require.NoError(t, repo.Insert(t.Context(), tx, newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusEscalated, nil)))

	t.Run("no filter counts everything", func(t *testing.T) {
		count, err := repo.Count(t.Context(), tx, repository.ListAlertsFilter{})
		require.NoError(t, err)
		assert.Equal(t, 3, count)
	})

	t.Run("count matches filtered list length, ignoring limit/offset", func(t *testing.T) {
		sev := domain.SeverityCritical
		count, err := repo.Count(t.Context(), tx, repository.ListAlertsFilter{Severity: &sev, Limit: 1, Offset: 0})
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("count reflects statuses OR filter", func(t *testing.T) {
		count, err := repo.Count(t.Context(), tx, repository.ListAlertsFilter{
			Statuses: []domain.AlertStatus{domain.AlertStatusInvestigating, domain.AlertStatusEscalated},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("count reflects severities OR filter", func(t *testing.T) {
		count, err := repo.Count(t.Context(), tx, repository.ListAlertsFilter{
			Severities: []domain.Severity{domain.SeverityCritical, domain.SeverityHigh},
		})
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})
}

// insertAlertWithGroupKey inserts an alert with an explicit
// WebhookEndpointID/GroupKey/ReceivedAt -- FindAndIncrementDuplicate's own
// three match conditions -- since newTestAlert doesn't set any of them.
func insertAlertWithGroupKey(t *testing.T, tx pgx.Tx, tenantID, endpointID uuid.UUID, groupKey string, receivedAt time.Time) *domain.Alert {
	t.Helper()
	repo := repository.NewAlertRepository()
	a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	a.WebhookEndpointID = &endpointID
	a.GroupKey = &groupKey
	a.ReceivedAt = receivedAt
	require.NoError(t, repo.Insert(t.Context(), tx, a))
	return a
}

func TestAlertRepository_FindAndIncrementDuplicate(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	endpointID := testutil.NewWebhookEndpoint(t, tenantID)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	original := insertAlertWithGroupKey(t, tx, tenantID, endpointID, "key-a", time.Now())

	t.Run("a match within the window increments and returns the existing alert", func(t *testing.T) {
		id, newCount, found, err := repo.FindAndIncrementDuplicate(t.Context(), tx, endpointID, "key-a", 30)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, original.ID, id)
		assert.Equal(t, 1, newCount)

		// A second call keeps incrementing, not resetting to 1.
		_, newCount2, found2, err := repo.FindAndIncrementDuplicate(t.Context(), tx, endpointID, "key-a", 30)
		require.NoError(t, err)
		assert.True(t, found2)
		assert.Equal(t, 2, newCount2)
	})

	t.Run("no row for that group key returns found=false, not an error", func(t *testing.T) {
		_, _, found, err := repo.FindAndIncrementDuplicate(t.Context(), tx, endpointID, "no-such-key", 30)
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("a match on a different webhook endpoint doesn't count", func(t *testing.T) {
		otherEndpointID := testutil.NewWebhookEndpoint(t, tenantID)
		_, _, found, err := repo.FindAndIncrementDuplicate(t.Context(), tx, otherEndpointID, "key-a", 30)
		require.NoError(t, err)
		assert.False(t, found, "the same group key on a different endpoint must not match")
	})

	t.Run("outside the window, no match", func(t *testing.T) {
		insertAlertWithGroupKey(t, tx, tenantID, endpointID, "key-old", time.Now().Add(-time.Hour))

		_, _, found, err := repo.FindAndIncrementDuplicate(t.Context(), tx, endpointID, "key-old", 30)
		require.NoError(t, err)
		assert.False(t, found, "an alert received an hour ago must not match a 30-minute window")
	})

	t.Run("within the window, a match", func(t *testing.T) {
		insertAlertWithGroupKey(t, tx, tenantID, endpointID, "key-recent", time.Now().Add(-5*time.Minute))

		_, _, found, err := repo.FindAndIncrementDuplicate(t.Context(), tx, endpointID, "key-recent", 30)
		require.NoError(t, err)
		assert.True(t, found)
	})
}

// TestAlertRepository_Correlated_ReflectsIncidentAlertLinks is the
// regression test for the "Correlated Alerts" bug: incident_id used to be
// read straight off the alerts table, but nothing ever wrote it (escalating
// an alert, and manually linking one via an incident's "Correlated Alerts"
// panel, both only ever insert into incident_alert_links) -- so the filter
// and the per-alert IncidentID field always looked empty even for alerts
// that were genuinely linked. Both are now computed from
// incident_alert_links at read time (see alertColumnsQualified's doc
// comment), so this links an alert the same way the real app does (via
// IncidentRepository.LinkAlert, never by writing alerts.incident_id
// directly) and asserts both the filter and Get reflect it.
func TestAlertRepository_Correlated_ReflectsIncidentAlertLinks(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alerts := repository.NewAlertRepository()
	incidents := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	linked := newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusOpen, nil)
	require.NoError(t, alerts.Insert(t.Context(), tx, linked))
	unlinked := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	require.NoError(t, alerts.Insert(t.Context(), tx, unlinked))

	inc := &domain.Incident{TenantID: tenantID, Title: "Suspicious login incident", Severity: domain.SeverityHigh, Priority: domain.PriorityP2, Phase: domain.PhaseNew, Tags: []string{}}
	require.NoError(t, incidents.Insert(t.Context(), tx, inc))
	require.NoError(t, incidents.LinkAlert(t.Context(), tx, inc.ID, linked.ID, tenantID))

	t.Run("Get returns the linked incident's id", func(t *testing.T) {
		got, err := alerts.Get(t.Context(), tx, linked.ID)
		require.NoError(t, err)
		require.NotNil(t, got.IncidentID)
		assert.Equal(t, inc.ID, *got.IncidentID)
	})

	t.Run("Get returns nil for an unlinked alert", func(t *testing.T) {
		got, err := alerts.Get(t.Context(), tx, unlinked.ID)
		require.NoError(t, err)
		assert.Nil(t, got.IncidentID)
	})

	t.Run("Correlated=true returns only the linked alert", func(t *testing.T) {
		correlated := true
		list, err := alerts.List(t.Context(), tx, repository.ListAlertsFilter{Correlated: &correlated})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, linked.ID, list[0].ID)
	})

	t.Run("Correlated=false excludes the linked alert", func(t *testing.T) {
		correlated := false
		list, err := alerts.List(t.Context(), tx, repository.ListAlertsFilter{Correlated: &correlated})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, unlinked.ID, list[0].ID)
	})

	t.Run("unlinking clears it back out of Correlated=true", func(t *testing.T) {
		require.NoError(t, incidents.UnlinkAlert(t.Context(), tx, inc.ID, linked.ID))

		correlated := true
		list, err := alerts.List(t.Context(), tx, repository.ListAlertsFilter{Correlated: &correlated})
		require.NoError(t, err)
		assert.Empty(t, list)
	})
}

func TestAlertRepository_UpdateStatus(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityMedium, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, a))

	t.Run("stampAcknowledged sets acknowledged_at the first time", func(t *testing.T) {
		require.NoError(t, repo.UpdateStatus(t.Context(), tx, a.ID, domain.AlertStatusInvestigating, true))
		got, err := repo.Get(t.Context(), tx, a.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AlertStatusInvestigating, got.Status)
		require.NotNil(t, got.AcknowledgedAt)
		firstAck := *got.AcknowledgedAt

		// A second status change with stampAcknowledged=true must not move
		// acknowledged_at -- it's a "first time it left open" timestamp, not
		// "most recently touched".
		require.NoError(t, repo.UpdateStatus(t.Context(), tx, a.ID, domain.AlertStatusEscalated, true))
		got2, err := repo.Get(t.Context(), tx, a.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.AlertStatusEscalated, got2.Status)
		require.NotNil(t, got2.AcknowledgedAt)
		assert.Equal(t, firstAck, *got2.AcknowledgedAt)
	})
}

func TestAlertRepository_UpdateTags(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, []string{"old-tag"})
	require.NoError(t, repo.Insert(t.Context(), tx, a))

	require.NoError(t, repo.UpdateTags(t.Context(), tx, a.ID, []string{"new-tag-1", "new-tag-2"}))
	got, err := repo.Get(t.Context(), tx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"new-tag-1", "new-tag-2"}, got.Tags)
}

func TestAlertRepository_Close(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusInvestigating, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, a))

	attachmentURL := "https://cdn.example.com/screenshot.png"
	require.NoError(t, repo.Close(t.Context(), tx, a.ID, domain.CloseAlertInput{
		Classification: domain.ClassificationFalsePositive,
		Comment:        "Confirmed benign",
		AttachmentURL:  &attachmentURL,
	}))

	got, err := repo.Get(t.Context(), tx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AlertStatusClosed, got.Status)
	require.NotNil(t, got.Classification)
	assert.Equal(t, domain.ClassificationFalsePositive, *got.Classification)
	require.NotNil(t, got.CloseComment)
	assert.Equal(t, "Confirmed benign", *got.CloseComment)
	assert.NotNil(t, got.ClosedAt)
}

func TestAlertRepository_UpdateSeverity(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, a))

	require.NoError(t, repo.UpdateSeverity(t.Context(), tx, a.ID, domain.SeverityCritical))
	got, err := repo.Get(t.Context(), tx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.SeverityCritical, got.Severity)
	assert.Equal(t, domain.SeverityLow, got.OriginalSeverity, "UpdateSeverity must never touch original_severity")
}

func TestAlertRepository_UpdateAssignee(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, a))

	got, err := repo.Get(t.Context(), tx, a.ID)
	require.NoError(t, err)
	assert.Nil(t, got.AssignedAnalystID, "a freshly ingested alert starts unassigned")
	assert.Nil(t, got.AssignedAnalystName)

	require.NoError(t, repo.UpdateAssignee(t.Context(), tx, a.ID, &analystID))
	got, err = repo.Get(t.Context(), tx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got.AssignedAnalystID)
	assert.Equal(t, analystID, *got.AssignedAnalystID)
	require.NotNil(t, got.AssignedAnalystName)

	t.Run("clearing the assignee sets it back to nil", func(t *testing.T) {
		require.NoError(t, repo.UpdateAssignee(t.Context(), tx, a.ID, nil))
		got, err := repo.Get(t.Context(), tx, a.ID)
		require.NoError(t, err)
		assert.Nil(t, got.AssignedAnalystID)
		assert.Nil(t, got.AssignedAnalystName)
	})

	t.Run("List also resolves the assignee name", func(t *testing.T) {
		require.NoError(t, repo.UpdateAssignee(t.Context(), tx, a.ID, &analystID))
		list, err := repo.List(t.Context(), tx, repository.ListAlertsFilter{})
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.NotNil(t, list[0].AssignedAnalystName)
	})
}

func TestAlertRepository_LinkUnlink(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, a))
	b := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, b))

	t.Run("linking is symmetric", func(t *testing.T) {
		require.NoError(t, repo.LinkAlert(t.Context(), tx, a.ID, b.ID, tenantID, actorID))

		aLinks, err := repo.ListLinkedAlerts(t.Context(), tx, a.ID)
		require.NoError(t, err)
		require.Len(t, aLinks, 1)
		assert.Equal(t, b.ID, aLinks[0].ID)

		bLinks, err := repo.ListLinkedAlerts(t.Context(), tx, b.ID)
		require.NoError(t, err)
		require.Len(t, bLinks, 1)
		assert.Equal(t, a.ID, bLinks[0].ID)
	})

	t.Run("linking twice is a no-op, not a conflict error", func(t *testing.T) {
		require.NoError(t, repo.LinkAlert(t.Context(), tx, a.ID, b.ID, tenantID, actorID))
	})

	t.Run("unlink removes both directions", func(t *testing.T) {
		require.NoError(t, repo.UnlinkAlert(t.Context(), tx, a.ID, b.ID))

		aLinks, err := repo.ListLinkedAlerts(t.Context(), tx, a.ID)
		require.NoError(t, err)
		assert.Empty(t, aLinks)
		bLinks, err := repo.ListLinkedAlerts(t.Context(), tx, b.ID)
		require.NoError(t, err)
		assert.Empty(t, bLinks)
	})
}

func TestAlertRepository_Events(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityLow, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, a))

	e := &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventStatusChanged,
		ActorType: domain.ActorUser, Data: json.RawMessage(`{"from":"open","to":"investigating"}`),
	}
	require.NoError(t, repo.InsertEvent(t.Context(), tx, e))
	require.NotZero(t, e.ID)
	assert.NotZero(t, e.CreatedAt)
}

func TestAlertRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	a := newTestAlert(tenantA, domain.SeverityHigh, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), txA, a))

	txB := testutil.BeginTx(t, pool, tenantB)
	list, err := repo.List(t.Context(), txB, repository.ListAlertsFilter{})
	require.NoError(t, err)
	assert.Empty(t, list, "RLS must prevent tenant B from seeing tenant A's alerts")

	got, err := repo.Get(t.Context(), txB, a.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must hide a cross-tenant alert even by direct id lookup")
}

func TestAlertRepository_InsertListComments(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusOpen, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, a))

	t.Run("no comments yet", func(t *testing.T) {
		comments, err := repo.ListComments(t.Context(), tx, a.ID)
		require.NoError(t, err)
		assert.Empty(t, comments)
	})

	authorID := testutil.NewUser(t, tenantID, "analyst", nil)
	attachmentURL := "https://example.com/evidence.png"
	c := &domain.AlertComment{
		AlertID: a.ID, TenantID: tenantID, AuthorID: authorID,
		AuthorName: "Diego Costa", Body: "escalating to IR", AttachmentURL: &attachmentURL,
	}
	require.NoError(t, repo.InsertComment(t.Context(), tx, c))
	assert.NotEqual(t, uuid.Nil, c.ID)
	assert.False(t, c.CreatedAt.IsZero())

	t.Run("the comment is listed back, oldest first", func(t *testing.T) {
		comments, err := repo.ListComments(t.Context(), tx, a.ID)
		require.NoError(t, err)
		require.Len(t, comments, 1)
		assert.Equal(t, "escalating to IR", comments[0].Body)
		assert.Equal(t, "Diego Costa", comments[0].AuthorName)
		require.NotNil(t, comments[0].AttachmentURL)
		assert.Equal(t, attachmentURL, *comments[0].AttachmentURL)
	})
}
