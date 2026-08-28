package repository_test

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

// TestAuditRepository_ExportEvents_IncludesAllFourSources guards the
// four-branch UNION shape (alert_events, alert_comments, incident_events,
// incident_comments) -- each source must surface with the right Kind and a
// synthesized "comment_added" EventType for the two comment branches, same
// as DashboardRepository.RecentActivity's equivalent union.
func TestAuditRepository_ExportEvents_IncludesAllFourSources(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()
	auditRepo := repository.NewAuditRepository()
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
	require.NoError(t, alertRepo.InsertComment(t.Context(), tx, &domain.AlertComment{
		AlertID: a.ID, TenantID: tenantID, AuthorID: actorID, AuthorName: "Marina Alves",
		Body: "Confirmed source IP is a known scanner.",
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
	require.NoError(t, incidentRepo.InsertComment(t.Context(), tx, &domain.IncidentComment{
		IncidentID: inc.ID, TenantID: tenantID, AuthorID: actorID, AuthorName: "Diego Costa",
		Body: "Backup from Jul 20 verified clean.",
	}))

	events, err := auditRepo.ExportEvents(t.Context(), tx, nil, "", 100)
	require.NoError(t, err)
	require.Len(t, events, 4)

	kinds := map[string]int{}
	eventTypes := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
		eventTypes[e.EventType]++
	}
	assert.Equal(t, 2, kinds["alert"])
	assert.Equal(t, 2, kinds["incident"])
	assert.Equal(t, 2, eventTypes["comment_added"], "both comment branches synthesize comment_added")
}

// TestAuditRepository_ExportEvents_KeysetPagination guards the actual
// export-cursor behavior ExportEvents adds on top of RecentActivity's
// "most recent N" shape: strictly-after ordering on (created_at, event_id),
// resumable across calls. Timestamps are set explicitly via a follow-up
// update because every insert inside one test transaction shares the same
// now() (Postgres' transaction timestamp, not wall-clock per statement).
func TestAuditRepository_ExportEvents_KeysetPagination(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	auditRepo := repository.NewAuditRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var firstEventID string
	for i := 0; i < 3; i++ {
		e := &domain.AlertEvent{
			AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
			ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
		}
		require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, e))
		_, err := tx.Exec(t.Context(), `update alert_events set created_at = $1 where id = $2`,
			base.Add(time.Duration(i)*time.Minute), e.ID)
		require.NoError(t, err)
		if i == 0 {
			firstEventID = strconv.FormatInt(e.ID, 10)
		}
	}

	page1, err := auditRepo.ExportEvents(t.Context(), tx, nil, "", 2)
	require.NoError(t, err)
	require.Len(t, page1, 2, "first page respects the limit")
	assert.Equal(t, firstEventID, page1[0].EventID)
	assert.True(t, page1[0].CreatedAt.Before(page1[1].CreatedAt), "oldest first")

	last := page1[len(page1)-1]
	page2, err := auditRepo.ExportEvents(t.Context(), tx, &last.CreatedAt, last.EventID, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1, "only the third event remains after the cursor")
	assert.True(t, page2[0].CreatedAt.After(last.CreatedAt))

	page3, err := auditRepo.ExportEvents(t.Context(), tx, &page2[0].CreatedAt, page2[0].EventID, 2)
	require.NoError(t, err)
	assert.Empty(t, page3, "nothing left after the last event's cursor")
}

func TestAuditRepository_ExportEvents_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	auditRepo := repository.NewAuditRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	a := &domain.Alert{
		TenantID: tenantA, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), txA, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), txA, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantA, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	events, err := auditRepo.ExportEvents(t.Context(), txB, nil, "", 100)
	require.NoError(t, err)
	assert.Empty(t, events, "RLS must prevent tenant B from exporting tenant A's audit events")
}
