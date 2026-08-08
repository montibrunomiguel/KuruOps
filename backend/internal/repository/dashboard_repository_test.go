package repository_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestDashboardRepository_Stats(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("a fresh tenant with zero data reports all zeros, not an error", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		assert.Zero(t, stats.OpenAlerts)
		assert.Zero(t, stats.CriticalAlerts)
		assert.Zero(t, stats.HighAlerts)
		assert.Zero(t, stats.ActiveIncidents)
		assert.Zero(t, stats.SLABreachedCount)
	})

	mustInsertAlert := func(severity domain.Severity, status domain.AlertStatus) *domain.Alert {
		a := &domain.Alert{
			TenantID: tenantID, Title: "t", Source: "test",
			Severity: severity, OriginalSeverity: severity, Status: domain.AlertStatusOpen,
			Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
		}
		require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
		if status == domain.AlertStatusClosed {
			require.NoError(t, alertRepo.Close(t.Context(), tx, a.ID, domain.CloseAlertInput{
				Classification: domain.ClassificationTruePositive,
				Comment:        "closed for dashboard stats test",
			}))
		} else if status != domain.AlertStatusOpen {
			require.NoError(t, alertRepo.UpdateStatus(t.Context(), tx, a.ID, status, true))
		}
		return a
	}
	mustInsertAlert(domain.SeverityCritical, domain.AlertStatusOpen)
	mustInsertAlert(domain.SeverityCritical, domain.AlertStatusClosed)
	mustInsertAlert(domain.SeverityLow, domain.AlertStatusInvestigating)
	mustInsertAlert(domain.SeverityHigh, domain.AlertStatusOpen)
	mustInsertAlert(domain.SeverityHigh, domain.AlertStatusClosed)

	t.Run("open alert counts reflect live, uncommitted state -- not just what's in the materialized views", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		assert.Equal(t, 3, stats.OpenAlerts, "3 non-closed alerts (open critical + investigating low + open high)")
		assert.Equal(t, 1, stats.CriticalAlerts, "only the still-open critical alert counts, not the closed one")
		assert.Equal(t, 1, stats.HighAlerts, "only the still-open high alert counts, not the closed one")
	})

	t.Run("breakdowns count every alert regardless of status", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		assert.Equal(t, 2, stats.AlertsBySeverity["critical"])
		assert.Equal(t, 1, stats.AlertsBySeverity["low"])
		assert.Equal(t, 2, stats.AlertsBySeverity["high"])
		assert.Equal(t, 2, stats.AlertStatusDistribution["open"])
		assert.Equal(t, 2, stats.AlertStatusDistribution["closed"])
		assert.Equal(t, 1, stats.AlertStatusDistribution["investigating"])
	})

	t.Run("alert trend is empty for a tenant whose data hasn't been through a materialized view refresh yet", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		assert.Empty(t, stats.AlertTrend, "mv_alert_daily_stats only sees committed data as of its last refresh")
	})

	t.Run("incident trend is empty for a tenant whose data hasn't been through a materialized view refresh yet", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		assert.Empty(t, stats.IncidentTrend, "mv_incident_daily_stats only sees committed data as of its last refresh")
	})
}

// TestDashboardRepository_Stats_AllowedTagsScoping guards the dashboard
// tag-access bug: a user restricted to a subset of tags must only see
// aggregates (counts + breakdowns) for alerts/incidents within that scope,
// not tenant-wide figures -- previously StatsFilter had no AllowedTags
// field at all, so every dashboard card leaked tenant-wide data regardless
// of the caller's tag scope.
func TestDashboardRepository_Stats_AllowedTagsScoping(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inScope := &domain.Alert{
		TenantID: tenantID, Title: "in scope", Source: "test",
		Severity: domain.SeverityCritical, OriginalSeverity: domain.SeverityCritical, Status: domain.AlertStatusOpen,
		Tags: []string{"ifood"}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, inScope))
	outOfScope := &domain.Alert{
		TenantID: tenantID, Title: "out of scope", Source: "test",
		Severity: domain.SeverityCritical, OriginalSeverity: domain.SeverityCritical, Status: domain.AlertStatusOpen,
		Tags: []string{"other-company"}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, outOfScope))

	inScopeInc := &domain.Incident{
		TenantID: tenantID, Title: "in scope", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
		Phase: domain.PhaseNew, Tags: []string{"ifood"},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, inScopeInc))
	outOfScopeInc := &domain.Incident{
		TenantID: tenantID, Title: "out of scope", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
		Phase: domain.PhaseNew, Tags: []string{"other-company"},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, outOfScopeInc))

	t.Run("unrestricted (nil AllowedTags) sees everything", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		assert.Equal(t, 2, stats.OpenAlerts)
		assert.Equal(t, 2, stats.ActiveIncidents)
	})

	t.Run("scoped to one tag only sees that tag's alerts/incidents", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{AllowedTags: []string{"ifood"}})
		require.NoError(t, err)
		assert.Equal(t, 1, stats.OpenAlerts, "only the ifood-tagged alert")
		assert.Equal(t, 1, stats.CriticalAlerts)
		assert.Equal(t, 1, stats.ActiveIncidents, "only the ifood-tagged incident")
		assert.Equal(t, 1, stats.AlertsBySeverity["critical"], "breakdowns are scoped too, not just the top-line counts")
	})

	t.Run("scoped to an unrelated tag sees nothing", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{AllowedTags: []string{"unrelated"}})
		require.NoError(t, err)
		assert.Zero(t, stats.OpenAlerts)
		assert.Zero(t, stats.ActiveIncidents)
	})
}

// TestDashboardRepository_Stats_IncidentCountsAreLive guards against a
// regression to mv_incident_kpis-backed counts: cmd/worker only refreshes
// that view once a minute, so a newly created/closed incident would not
// show up on the Dashboard for up to 60s -- reported live as "the incident
// I just created didn't reflect on the dashboard".
func TestDashboardRepository_Stats_IncidentCountsAreLive(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	incidentRepo := repository.NewIncidentRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	mustInsertIncident := func(priority domain.IncidentPriority, phase domain.IncidentPhase, slaBreached bool) {
		inc := &domain.Incident{
			TenantID: tenantID, Title: "t", Severity: domain.SeverityHigh, Priority: priority, Phase: domain.PhaseNew,
			Tags: []string{},
		}
		require.NoError(t, incidentRepo.Insert(t.Context(), tx, inc))
		if phase != domain.PhaseNew {
			require.NoError(t, incidentRepo.UpdatePhase(t.Context(), tx, inc.ID, phase))
		}
		if slaBreached {
			_, err := tx.Exec(t.Context(), `update incidents set sla_breached = true where id = $1`, inc.ID)
			require.NoError(t, err)
		}
	}
	mustInsertIncident(domain.PriorityP1, domain.PhaseNew, false)
	mustInsertIncident(domain.PriorityP2, domain.PhasePostIncident, false)
	mustInsertIncident(domain.PriorityP3, domain.PhaseContainment, true)

	stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
	require.NoError(t, err)
	assert.Equal(t, 2, stats.ActiveIncidents, "2 incidents not in post_incident phase, uncommitted or not")
	assert.Equal(t, 1, stats.SLABreachedCount)
	assert.Equal(t, 1, stats.P1OpenCount)
}

func TestDashboardRepository_RecentActivity(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	events, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "alert", events[0].Kind)
	assert.Equal(t, a.ID, events[0].ContextID)
	assert.Equal(t, "Suspicious login", events[0].ContextTitle)
	assert.Equal(t, domain.AlertEventReceived, domain.AlertEventType(events[0].EventType))

	t.Run("limit caps the result", func(t *testing.T) {
		require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
			AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventStatusChanged,
			ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
		}))
		limited, err := dashboardRepo.RecentActivity(t.Context(), tx, 1, "", nil, nil, nil)
		require.NoError(t, err)
		require.Len(t, limited, 1)
	})
}

// TestDashboardRepository_RecentActivity_KindFilter guards the Alerts
// dashboard tab's data-leak fix: kind="alert" must never surface incident
// events, and vice versa, even though both live in the same feed.
func TestDashboardRepository_RecentActivity_KindFilter(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	inc := &domain.Incident{
		TenantID: tenantID, Title: "Ransomware suspected", Severity: domain.SeverityCritical,
		Priority: domain.PriorityP1, Phase: domain.PhaseNew, Tags: []string{},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, inc))
	require.NoError(t, incidentRepo.InsertEvent(t.Context(), tx, &domain.IncidentEvent{
		IncidentID: inc.ID, TenantID: tenantID, EventType: domain.IncidentEventCreated,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	alertOnly, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "alert", nil, nil, nil)
	require.NoError(t, err)
	for _, e := range alertOnly {
		assert.Equal(t, "alert", e.Kind)
	}
	require.Len(t, alertOnly, 1)

	incidentOnly, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "incident", nil, nil, nil)
	require.NoError(t, err)
	for _, e := range incidentOnly {
		assert.Equal(t, "incident", e.Kind)
	}
	require.Len(t, incidentOnly, 1)

	both, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, both, 2)
}

// TestDashboardRepository_RecentActivity_AllowedTagsScoping guards the same
// dashboard tag-access bug as Stats, but for the activity feed: an
// out-of-scope alert/incident's events must not leak into a tag-restricted
// user's feed.
func TestDashboardRepository_RecentActivity_AllowedTagsScoping(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inScope := &domain.Alert{
		TenantID: tenantID, Title: "in scope", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{"ifood"}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, inScope))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: inScope.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	outOfScope := &domain.Alert{
		TenantID: tenantID, Title: "out of scope", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{"other-company"}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, outOfScope))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: outOfScope.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	outOfScopeInc := &domain.Incident{
		TenantID: tenantID, Title: "out of scope incident", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
		Phase: domain.PhaseNew, Tags: []string{"other-company"},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, outOfScopeInc))
	require.NoError(t, incidentRepo.InsertEvent(t.Context(), tx, &domain.IncidentEvent{
		IncidentID: outOfScopeInc.ID, TenantID: tenantID, EventType: domain.IncidentEventCreated,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	events, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", []string{"ifood"}, nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1, "only the ifood-tagged alert's event should surface")
	assert.Equal(t, inScope.ID, events[0].ContextID)

	unrestricted, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", nil, nil, nil)
	require.NoError(t, err)
	assert.Len(t, unrestricted, 3, "nil allowedTags sees everything")
}

// TestDashboardRepository_RecentActivity_IncludesComments guards the third
// UNION ALL branch: incident_comments has no event_type of its own (it's
// not part of the append-only *_events audit log), so RecentActivity
// synthesizes "comment_added" for it -- otherwise a team note would never
// show up in the feed at all.
func TestDashboardRepository_RecentActivity_IncludesComments(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	incidentRepo := repository.NewIncidentRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := &domain.Incident{
		TenantID: tenantID, Title: "Ransomware suspected", Severity: domain.SeverityCritical,
		Priority: domain.PriorityP1, Phase: domain.PhaseNew, Tags: []string{},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, inc))
	require.NoError(t, incidentRepo.InsertComment(t.Context(), tx, &domain.IncidentComment{
		IncidentID: inc.ID, TenantID: tenantID, AuthorID: actorID, AuthorName: "Diego Costa",
		Body: "Backup from Jul 20 verified clean.",
	}))

	events, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "incident", events[0].Kind)
	assert.Equal(t, "comment_added", events[0].EventType)
	assert.Equal(t, inc.ID, events[0].ContextID)
	assert.Equal(t, "Ransomware suspected", events[0].ContextTitle)

	var data map[string]string
	require.NoError(t, json.Unmarshal(events[0].Data, &data))
	assert.Equal(t, "Diego Costa", data["authorName"])
}

// TestDashboardRepository_RecentActivity_IncludesAlertComments is the
// alert-side equivalent -- guards the alert_comments UNION branch added
// alongside AlertService.AddComment, and confirms it surfaces under
// kind="alert" specifically (not just kind="").
func TestDashboardRepository_RecentActivity_IncludesAlertComments(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	alertRepo := repository.NewAlertRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertComment(t.Context(), tx, &domain.AlertComment{
		AlertID: a.ID, TenantID: tenantID, AuthorID: actorID, AuthorName: "Marina Alves",
		Body: "Confirmed source IP is a known scanner.",
	}))

	events, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "alert", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "alert", events[0].Kind)
	assert.Equal(t, "comment_added", events[0].EventType)
	assert.Equal(t, a.ID, events[0].ContextID)

	var data map[string]string
	require.NoError(t, json.Unmarshal(events[0].Data, &data))
	assert.Equal(t, "Marina Alves", data["authorName"])

	incidentOnly, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "incident", nil, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, incidentOnly, "an alert comment must never leak into the incident-only feed")
}

// TestDashboardRepository_Stats_SinceFilter guards the Dashboard's
// time-range filter: an alert received before the cutoff must not count
// toward the live figures, an incident opened before it must not either.
func TestDashboardRepository_Stats_SinceFilter(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	cutoff := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

	oldAlert := &domain.Alert{
		TenantID: tenantID, Title: "old", Source: "test",
		Severity: domain.SeverityCritical, OriginalSeverity: domain.SeverityCritical, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: cutoff.Add(-24 * time.Hour),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, oldAlert))
	newAlert := &domain.Alert{
		TenantID: tenantID, Title: "new", Source: "test",
		Severity: domain.SeverityCritical, OriginalSeverity: domain.SeverityCritical, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: cutoff.Add(24 * time.Hour),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, newAlert))

	oldIncident := &domain.Incident{
		TenantID: tenantID, Title: "old incident", Severity: domain.SeverityCritical,
		Priority: domain.PriorityP1, Phase: domain.PhaseNew, Tags: []string{},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, oldIncident))
	_, err := tx.Exec(t.Context(), `update incidents set opened_at = $1 where id = $2`, cutoff.Add(-24*time.Hour), oldIncident.ID)
	require.NoError(t, err)
	newIncident := &domain.Incident{
		TenantID: tenantID, Title: "new incident", Severity: domain.SeverityCritical,
		Priority: domain.PriorityP1, Phase: domain.PhaseNew, Tags: []string{},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, newIncident))
	_, err = tx.Exec(t.Context(), `update incidents set opened_at = $1 where id = $2`, cutoff.Add(24*time.Hour), newIncident.ID)
	require.NoError(t, err)

	t.Run("no Since -- both count", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		assert.Equal(t, 2, stats.OpenAlerts)
		assert.Equal(t, 2, stats.ActiveIncidents)
	})

	t.Run("Since the cutoff -- only the new ones count", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{Since: &cutoff})
		require.NoError(t, err)
		assert.Equal(t, 1, stats.OpenAlerts, "only the alert received after the cutoff")
		assert.Equal(t, 1, stats.ActiveIncidents, "only the incident opened after the cutoff")
	})

	t.Run("Until the cutoff -- only the old ones count", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{Until: &cutoff})
		require.NoError(t, err)
		assert.Equal(t, 1, stats.OpenAlerts, "only the alert received before the cutoff")
		assert.Equal(t, 1, stats.ActiveIncidents, "only the incident opened before the cutoff")
	})

	t.Run("Since and Until together -- a narrow window excludes both", func(t *testing.T) {
		narrowSince := cutoff.Add(-1 * time.Hour)
		narrowUntil := cutoff.Add(1 * time.Hour)
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{Since: &narrowSince, Until: &narrowUntil})
		require.NoError(t, err)
		assert.Equal(t, 0, stats.OpenAlerts, "neither alert falls inside this narrow window")
		assert.Equal(t, 0, stats.ActiveIncidents, "neither incident falls inside this narrow window")
	})
}

// TestDashboardRepository_RecentActivity_SinceFilter mirrors the Stats
// version, for the activity feed -- filtered on each event's own
// created_at, not the parent alert's received_at (see RecentActivity's doc
// comment).
func TestDashboardRepository_RecentActivity_SinceFilter(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))

	cutoff := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	oldEvent := &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, oldEvent))
	_, err := tx.Exec(t.Context(), `update alert_events set created_at = $1 where id = $2`, cutoff.Add(-time.Hour), oldEvent.ID)
	require.NoError(t, err)

	newEvent := &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventStatusChanged,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, newEvent))
	_, err = tx.Exec(t.Context(), `update alert_events set created_at = $1 where id = $2`, cutoff.Add(time.Hour), newEvent.ID)
	require.NoError(t, err)

	all, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, all, 2)

	sinceCutoff, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", nil, &cutoff, nil)
	require.NoError(t, err)
	require.Len(t, sinceCutoff, 1, "only the event after the cutoff")
	assert.Equal(t, domain.AlertEventStatusChanged, domain.AlertEventType(sinceCutoff[0].EventType))

	untilCutoff, err := dashboardRepo.RecentActivity(t.Context(), tx, 10, "", nil, nil, &cutoff)
	require.NoError(t, err)
	require.Len(t, untilCutoff, 1, "only the event before the cutoff")
	assert.Equal(t, domain.AlertEventReceived, domain.AlertEventType(untilCutoff[0].EventType))
}

// TestDashboardRepository_Stats_AlertsByAnalyst guards the new identity-keyed
// breakdown: an assigned alert groups under its analyst's name, an
// unassigned one groups into the nil-ID/empty-Name bucket, and
// AssignedAnalystID narrows every other alert-derived figure the same way
// AlertSeverity etc. already do.
func TestDashboardRepository_Stats_AlertsByAnalyst(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	analystA := testutil.NewUser(t, tenantID, "analyst", nil)
	analystB := testutil.NewUser(t, tenantID, "analyst", nil)
	alertRepo := repository.NewAlertRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	mustInsert := func(analyst *uuid.UUID) {
		a := &domain.Alert{
			TenantID: tenantID, Title: "t", Source: "test",
			Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
			Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(), AssignedAnalystID: analyst,
		}
		require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	}
	mustInsert(&analystA)
	mustInsert(&analystA)
	mustInsert(&analystB)
	mustInsert(nil)

	t.Run("groups by analyst, unassigned bucket has a nil id", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		require.Len(t, stats.AlertsByAnalyst, 3)

		byID := map[string]domain.NamedCount{}
		for _, c := range stats.AlertsByAnalyst {
			key := "unassigned"
			if c.ID != nil {
				key = c.ID.String()
			}
			byID[key] = c
		}
		assert.Equal(t, 2, byID[analystA.String()].Count)
		assert.Equal(t, 1, byID[analystB.String()].Count)
		assert.Equal(t, 1, byID["unassigned"].Count)
		assert.Empty(t, byID["unassigned"].Name, "unassigned bucket carries no server-rendered label")
	})

	t.Run("AssignedAnalystID narrows every other alert-derived figure too", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{AssignedAnalystID: &analystA})
		require.NoError(t, err)
		assert.Equal(t, 2, stats.OpenAlerts)
	})
}

// TestDashboardRepository_Stats_IncidentsByCommander mirrors the alert-side
// test above, for the incident_role_assignments-backed commander breakdown.
func TestDashboardRepository_Stats_IncidentsByCommander(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	commanderA := testutil.NewUser(t, tenantID, "analyst", nil)
	commanderB := testutil.NewUser(t, tenantID, "analyst", nil)
	incidentRepo := repository.NewIncidentRepository()
	dashboardRepo := repository.NewDashboardRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	mustInsert := func(commander *uuid.UUID) uuid.UUID {
		inc := &domain.Incident{
			TenantID: tenantID, Title: "t", Severity: domain.SeverityCritical,
			Priority: domain.PriorityP1, Phase: domain.PhaseNew, Tags: []string{},
		}
		require.NoError(t, incidentRepo.Insert(t.Context(), tx, inc))
		if commander != nil {
			require.NoError(t, incidentRepo.SetRole(t.Context(), tx, inc.ID, tenantID, domain.RoleCommander, []uuid.UUID{*commander}))
		}
		return inc.ID
	}
	mustInsert(&commanderA)
	mustInsert(&commanderA)
	incWithB := mustInsert(&commanderB)
	mustInsert(nil)

	t.Run("groups by commander, no-commander bucket has a nil id", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{})
		require.NoError(t, err)
		require.Len(t, stats.IncidentsByCommander, 3)

		byID := map[string]domain.NamedCount{}
		for _, c := range stats.IncidentsByCommander {
			key := "none"
			if c.ID != nil {
				key = c.ID.String()
			}
			byID[key] = c
		}
		assert.Equal(t, 2, byID[commanderA.String()].Count)
		assert.Equal(t, 1, byID[commanderB.String()].Count)
		assert.Equal(t, 1, byID["none"].Count)
	})

	t.Run("CommanderID narrows every other incident-derived figure too", func(t *testing.T) {
		stats, err := dashboardRepo.Stats(t.Context(), tx, tenantID, repository.StatsFilter{CommanderID: &commanderB})
		require.NoError(t, err)
		assert.Equal(t, 1, stats.ActiveIncidents)
	})

	t.Run("ListIncidentsFilter.CommanderID narrows the plain incidents list too", func(t *testing.T) {
		list, err := incidentRepo.List(t.Context(), tx, repository.ListIncidentsFilter{CommanderID: &commanderB})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, incWithB, list[0].ID)
	})
}
