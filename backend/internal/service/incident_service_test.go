package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func newIncidentServices(t *testing.T) (*db.Pool, *service.IncidentService, *service.TagService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tagSvc := service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository())
	incSvc := service.NewIncidentService(pool, repository.NewIncidentRepository(), tagSvc, repository.NewUserRepository(), service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository()))
	return pool, incSvc, tagSvc
}

func TestIncidentService_Create(t *testing.T) {
	_, incSvc, tagSvc := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	_, err := tagSvc.Create(t.Context(), tenantID, actorID, "ransomware", nil)
	require.NoError(t, err)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "Ransomware suspected", Description: "desc",
		Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
		Tags: []string{"ransomware", "not-registered"},
	})
	require.NoError(t, err)
	assert.Equal(t, domain.PhaseNew, inc.Phase)
	assert.Equal(t, []string{"ransomware"}, inc.Tags, "unregistered tags are dropped at creation too")

	t.Run("creation is recorded in status history and the event timeline", func(t *testing.T) {
		history, _, err := incSvc.StatusHistory(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, history, 1)
		assert.Equal(t, domain.PhaseNew, history[0].Phase)

		events, _, err := incSvc.Timeline(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, domain.IncidentEventCreated, events[0].EventType)
	})
}

func TestIncidentService_Create_Assignees(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	analystA := testutil.NewUser(t, tenantID, "analyst", nil)
	analystB := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("resolves every valid assignee", func(t *testing.T) {
		inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "Multi-analyst incident", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
			AssigneeIDs: []uuid.UUID{analystA, analystB},
		})
		require.NoError(t, err)
		require.Len(t, inc.Assignees, 2)

		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, got.Assignees, 2)
	})

	t.Run("no assignees -- empty slice, not an error", func(t *testing.T) {
		inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "Unassigned incident", Severity: domain.SeverityLow, Priority: domain.PriorityP4,
		})
		require.NoError(t, err)
		assert.Empty(t, inc.Assignees)
	})

	t.Run("rejects the whole request on an unknown assignee id", func(t *testing.T) {
		_, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "Bad incident", Severity: domain.SeverityLow, Priority: domain.PriorityP4,
			AssigneeIDs: []uuid.UUID{analystA, uuid.New()},
		})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestIncidentService_SetAssignees(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	analystA := testutil.NewUser(t, tenantID, "analyst", nil)
	analystB := testutil.NewUser(t, tenantID, "analyst", nil)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4,
	})
	require.NoError(t, err)

	t.Run("replaces the assignee set and records an event", func(t *testing.T) {
		require.NoError(t, incSvc.SetAssignees(t.Context(), tenantID, inc.ID, actorID, []uuid.UUID{analystA, analystB}, nil))
		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, got.Assignees, 2)

		events, _, err := incSvc.Timeline(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.NotEmpty(t, events)
		assert.Equal(t, domain.IncidentEventAssigneesChanged, events[len(events)-1].EventType)

		require.NoError(t, incSvc.SetAssignees(t.Context(), tenantID, inc.ID, actorID, []uuid.UUID{analystA}, nil))
		got, err = incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, got.Assignees, 1)
		assert.Equal(t, analystA, got.Assignees[0].ID)
	})

	t.Run("rejects an unknown assignee id", func(t *testing.T) {
		err := incSvc.SetAssignees(t.Context(), tenantID, inc.ID, actorID, []uuid.UUID{uuid.New()}, nil)
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("an out-of-scope incident reads as not found", func(t *testing.T) {
		err := incSvc.SetAssignees(t.Context(), tenantID, inc.ID, actorID, []uuid.UUID{analystA}, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

// TestIncidentService_SetRole guards the NIST-role validation layer on top
// of IncidentRepository.SetRole: single-assignee cardinality is rejected
// with a clear error before ever reaching the DB, replacing a role's set
// works the same "replace" way SetAssignees does, and both assign/unassign
// record a timeline event.
func TestIncidentService_SetRole(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	analystA := testutil.NewUser(t, tenantID, "analyst", nil)
	analystB := testutil.NewUser(t, tenantID, "analyst", nil)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4,
	})
	require.NoError(t, err)

	t.Run("rejects an unknown role", func(t *testing.T) {
		err := incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.IncidentRole("nope"), []uuid.UUID{analystA}, nil)
		assert.ErrorContains(t, err, "unknown incident role")
	})

	t.Run("rejects two people for a single-assignee role before touching the DB", func(t *testing.T) {
		err := incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RoleCommander, []uuid.UUID{analystA, analystB}, nil)
		assert.ErrorContains(t, err, "at most one person")
	})

	t.Run("assigns a single-assignee role and records role_assigned", func(t *testing.T) {
		require.NoError(t, incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RoleCommander, []uuid.UUID{analystA}, nil))
		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, got.Roles, 1)
		assert.Equal(t, domain.RoleCommander, got.Roles[0].Role)
		assert.Equal(t, analystA, got.Roles[0].User.ID)

		events, _, err := incSvc.Timeline(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.IncidentEventRoleAssigned, events[len(events)-1].EventType)
	})

	t.Run("replacing the Commander swaps, doesn't add a second", func(t *testing.T) {
		require.NoError(t, incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RoleCommander, []uuid.UUID{analystB}, nil))
		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, got.Roles, 1)
		assert.Equal(t, analystB, got.Roles[0].User.ID)
	})

	t.Run("multi-assignee role accepts several people", func(t *testing.T) {
		require.NoError(t, incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RoleIncidentHandler, []uuid.UUID{analystA, analystB}, nil))
		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		handlerCount := 0
		for _, r := range got.Roles {
			if r.Role == domain.RoleIncidentHandler {
				handlerCount++
			}
		}
		assert.Equal(t, 2, handlerCount)
	})

	t.Run("clearing a role records role_unassigned", func(t *testing.T) {
		require.NoError(t, incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RoleIncidentHandler, nil, nil))
		events, _, err := incSvc.Timeline(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.IncidentEventRoleUnassigned, events[len(events)-1].EventType)
	})

	t.Run("rejects an unknown assignee id", func(t *testing.T) {
		err := incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RolePrivacyOfficer, []uuid.UUID{uuid.New()}, nil)
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("an out-of-scope incident reads as not found", func(t *testing.T) {
		err := incSvc.SetRole(t.Context(), tenantID, inc.ID, actorID, domain.RolePrivacyOfficer, []uuid.UUID{analystA}, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestIncidentService_ChangePhase(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "t", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
	})
	require.NoError(t, err)

	t.Run("a same-phase call is a no-op, no duplicate event", func(t *testing.T) {
		require.NoError(t, incSvc.ChangePhase(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, nil))
		events, _, err := incSvc.Timeline(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		assert.Len(t, events, 1, "still just the 'created' event")
	})

	t.Run("a direct forward jump logs both phase_changed and phase_skipped", func(t *testing.T) {
		require.NoError(t, incSvc.ChangePhase(t.Context(), tenantID, inc.ID, actorID, domain.PhaseContainment, nil))

		events, _, err := incSvc.Timeline(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		var sawChanged, sawSkipped bool
		for _, e := range events {
			if e.EventType == domain.IncidentEventPhaseChanged {
				sawChanged = true
			}
			if e.EventType == domain.IncidentEventPhaseSkipped {
				sawSkipped = true
			}
		}
		assert.True(t, sawChanged)
		assert.True(t, sawSkipped, "new -> containment skips detection_analysis")
	})

	t.Run("Close moves the incident to post_incident", func(t *testing.T) {
		require.NoError(t, incSvc.Close(t.Context(), tenantID, inc.ID, actorID, nil))
		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.PhasePostIncident, got.Phase)
		assert.NotNil(t, got.ClosedAt)
	})

	t.Run("reaching post_incident via a plain ChangePhase does not close it -- only Close does", func(t *testing.T) {
		other, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "not closed via phase tracker alone", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
		})
		require.NoError(t, err)

		require.NoError(t, incSvc.ChangePhase(t.Context(), tenantID, other.ID, actorID, domain.PhasePostIncident, nil))
		got, err := incSvc.Get(t.Context(), tenantID, other.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.PhasePostIncident, got.Phase)
		assert.Nil(t, got.ClosedAt, "the phase tracker reaching post_incident must not silently close the incident")

		require.NoError(t, incSvc.Close(t.Context(), tenantID, other.ID, actorID, nil))
		got, err = incSvc.Get(t.Context(), tenantID, other.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, got.ClosedAt, "the explicit Close action must close it")

		events, _, err := incSvc.Timeline(t.Context(), tenantID, other.ID, nil)
		require.NoError(t, err)
		var sawClosed bool
		for _, e := range events {
			if e.EventType == domain.IncidentEventClosed {
				sawClosed = true
			}
		}
		assert.True(t, sawClosed, "Close must record a distinct 'closed' timeline event")
	})

	t.Run("an out-of-scope incident can't have its phase changed", func(t *testing.T) {
		err := incSvc.ChangePhase(t.Context(), tenantID, inc.ID, actorID, domain.PhaseRecovery, []string{"unrelated-tag"})
		assert.ErrorContains(t, err, "not found")
	})
}

func TestIncidentService_BulkChangePhase(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	i1, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "i1", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
	})
	require.NoError(t, err)
	i2, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "i2", Severity: domain.SeverityHigh, Priority: domain.PriorityP2,
	})
	require.NoError(t, err)

	t.Run("every id succeeds, and the phase actually changed", func(t *testing.T) {
		results, err := incSvc.BulkChangePhase(t.Context(), tenantID, actorID, []uuid.UUID{i1.ID, i2.ID}, domain.PhaseContainment, nil)
		require.NoError(t, err)
		require.Len(t, results, 2)
		for _, r := range results {
			assert.True(t, r.Success)
			assert.Empty(t, r.Error)
		}

		got, err := incSvc.Get(t.Context(), tenantID, i1.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.PhaseContainment, got.Phase)
	})

	t.Run("one bad id among good ones fails only that one", func(t *testing.T) {
		unknownID := uuid.New()
		results, err := incSvc.BulkChangePhase(t.Context(), tenantID, actorID, []uuid.UUID{i1.ID, unknownID}, domain.PhaseEradication, nil)
		require.NoError(t, err)
		require.Len(t, results, 2)

		byID := map[uuid.UUID]service.BulkResult{}
		for _, r := range results {
			byID[r.ID] = r
		}
		assert.True(t, byID[i1.ID].Success)
		assert.False(t, byID[unknownID].Success)
		assert.Contains(t, byID[unknownID].Error, "not found")

		got, err := incSvc.Get(t.Context(), tenantID, i1.ID, nil)
		require.NoError(t, err)
		assert.Equal(t, domain.PhaseEradication, got.Phase, "the good id's change still went through despite the other one failing")
	})

	t.Run("bulk-setting post_incident is rejected up front, for the whole request -- bulk-close is descoped", func(t *testing.T) {
		results, err := incSvc.BulkChangePhase(t.Context(), tenantID, actorID, []uuid.UUID{i1.ID, i2.ID}, domain.PhasePostIncident, nil)
		assert.ErrorContains(t, err, "use Close")
		assert.Nil(t, results)

		got, err := incSvc.Get(t.Context(), tenantID, i1.ID, nil)
		require.NoError(t, err)
		assert.NotEqual(t, domain.PhasePostIncident, got.Phase, "the whole-request rejection must happen before any incident is touched")
	})

	t.Run("empty ids returns an empty result set, not an error", func(t *testing.T) {
		results, err := incSvc.BulkChangePhase(t.Context(), tenantID, actorID, nil, domain.PhaseRecovery, nil)
		require.NoError(t, err)
		assert.Empty(t, results)
	})
}

func TestIncidentService_SetSeverityAndPriorityAndDescription(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
		Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4,
	})
	require.NoError(t, err)

	require.NoError(t, incSvc.SetSeverityAndPriority(t.Context(), tenantID, inc.ID, actorID, domain.SeverityCritical, domain.PriorityP1, nil))
	require.NoError(t, incSvc.UpdateDescription(t.Context(), tenantID, inc.ID, actorID, "new description", nil))

	got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.SeverityCritical, got.Severity)
	assert.Equal(t, domain.PriorityP1, got.Priority)
	assert.Equal(t, "new description", got.Description)
}

func TestIncidentService_SLADueAt(t *testing.T) {
	pool, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	slaSvc := service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository())

	t.Run("Create with no configured policy leaves sla_due_at nil", func(t *testing.T) {
		inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "no policy", Severity: domain.SeverityLow, Priority: domain.PriorityP4,
		})
		require.NoError(t, err)
		assert.Nil(t, inc.SLADueAt)
	})

	_, err := slaSvc.Save(t.Context(), tenantID, actorID, domain.SeverityCritical, domain.PriorityP1, 60)
	require.NoError(t, err)

	t.Run("Create with a configured policy sets sla_due_at", func(t *testing.T) {
		inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "with policy", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
		})
		require.NoError(t, err)
		require.NotNil(t, inc.SLADueAt)
		assert.WithinDuration(t, time.Now().Add(60*time.Minute), *inc.SLADueAt, 5*time.Second)
	})

	t.Run("SetSeverityAndPriority recomputes sla_due_at for the new pair, clearing it when unconfigured", func(t *testing.T) {
		inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{
			Title: "starts with policy", Severity: domain.SeverityCritical, Priority: domain.PriorityP1,
		})
		require.NoError(t, err)
		require.NotNil(t, inc.SLADueAt)

		require.NoError(t, incSvc.SetSeverityAndPriority(t.Context(), tenantID, inc.ID, actorID, domain.SeverityLow, domain.PriorityP4, nil))
		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		assert.Nil(t, got.SLADueAt, "moving to an unconfigured pair must clear the stale due date")

		_, err = slaSvc.Save(t.Context(), tenantID, actorID, domain.SeverityLow, domain.PriorityP4, 120)
		require.NoError(t, err)
		require.NoError(t, incSvc.SetSeverityAndPriority(t.Context(), tenantID, inc.ID, actorID, domain.SeverityLow, domain.PriorityP4, nil))
		got, err = incSvc.Get(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.NotNil(t, got.SLADueAt)
		assert.WithinDuration(t, time.Now().Add(120*time.Minute), *got.SLADueAt, 5*time.Second)
	})
}

func TestIncidentService_UpdateTags(t *testing.T) {
	_, incSvc, tagSvc := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	_, err := tagSvc.Create(t.Context(), tenantID, actorID, "insider-threat", nil)
	require.NoError(t, err)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4})
	require.NoError(t, err)

	require.NoError(t, incSvc.UpdateTags(t.Context(), tenantID, inc.ID, actorID, []string{"insider-threat", "unregistered"}, nil))
	got, err := incSvc.Get(t.Context(), tenantID, inc.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"insider-threat"}, got.Tags)
}

func TestIncidentService_CorrectPhaseTimestamp(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4})
	require.NoError(t, err)

	t.Run("empty reason is rejected before touching the database", func(t *testing.T) {
		err := incSvc.CorrectPhaseTimestamp(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, inc.OpenedAt, "", nil)
		assert.ErrorContains(t, err, "reason is required")
	})

	t.Run("a valid correction is recorded with an event", func(t *testing.T) {
		require.NoError(t, incSvc.CorrectPhaseTimestamp(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, inc.OpenedAt, "backdated per SOC log", nil))
		events, _, err := incSvc.Timeline(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		var found bool
		for _, e := range events {
			if e.EventType == domain.IncidentEventTimestampCorrected {
				found = true
			}
		}
		assert.True(t, found)
	})
}

func TestIncidentService_CommentsAndAlertLinks(t *testing.T) {
	pool, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)
	alertSvc := service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewPlaybookRepository())

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4})
	require.NoError(t, err)

	t.Run("comments", func(t *testing.T) {
		c, err := incSvc.AddComment(t.Context(), tenantID, inc.ID, actorID, "Analyst", "investigating now", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "investigating now", c.Body)

		comments, _, err := incSvc.Comments(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, comments, 1)
	})

	t.Run("alert linking", func(t *testing.T) {
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: testPayload}, nil, 0)
		require.NoError(t, err)

		require.NoError(t, incSvc.LinkAlert(t.Context(), tenantID, inc.ID, alert.ID, actorID, nil))
		linked, _, err := incSvc.LinkedAlerts(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, linked, 1)
		assert.Equal(t, alert.ID, linked[0].ID)

		require.NoError(t, incSvc.UnlinkAlert(t.Context(), tenantID, inc.ID, alert.ID, actorID, nil))
		linked, _, err = incSvc.LinkedAlerts(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, linked)
	})
}

func TestIncidentService_IOCs(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4})
	require.NoError(t, err)

	t.Run("adds and lists an IOC", func(t *testing.T) {
		ioc, err := incSvc.AddIOC(t.Context(), tenantID, inc.ID, actorID, "Analyst One", domain.IOCTypeIPAddress, "203.0.113.42", "C2 beacon", time.Now(), nil)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.42", ioc.Value)
		assert.Equal(t, "Analyst One", ioc.CreatedByName)

		iocs, _, err := incSvc.IOCs(t.Context(), tenantID, inc.ID, nil)
		require.NoError(t, err)
		require.Len(t, iocs, 1)
		assert.Equal(t, domain.IOCTypeIPAddress, iocs[0].Type)
	})

	t.Run("rejects an unknown type", func(t *testing.T) {
		_, err := incSvc.AddIOC(t.Context(), tenantID, inc.ID, actorID, "Analyst One", domain.IOCType("not_a_real_type"), "x", "", time.Now(), nil)
		assert.Error(t, err)
	})

	t.Run("rejects an empty value", func(t *testing.T) {
		_, err := incSvc.AddIOC(t.Context(), tenantID, inc.ID, actorID, "Analyst One", domain.IOCTypeURL, "", "", time.Now(), nil)
		assert.Error(t, err)
	})

	t.Run("rejects a zero identifiedAt", func(t *testing.T) {
		_, err := incSvc.AddIOC(t.Context(), tenantID, inc.ID, actorID, "Analyst One", domain.IOCTypeURL, "http://x", "", time.Time{}, nil)
		assert.Error(t, err)
	})
}

func TestIncidentService_GetVisibility(t *testing.T) {
	_, incSvc, _ := newIncidentServices(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	inc, err := incSvc.Create(t.Context(), tenantID, actorID, domain.CreateIncidentInput{Title: "t", Severity: domain.SeverityLow, Priority: domain.PriorityP4})
	require.NoError(t, err)

	t.Run("out-of-scope reads as not found", func(t *testing.T) {
		got, err := incSvc.Get(t.Context(), tenantID, inc.ID, []string{"unrelated-tag"})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("unknown id also returns nil", func(t *testing.T) {
		got, err := incSvc.Get(t.Context(), tenantID, uuid.New(), nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
