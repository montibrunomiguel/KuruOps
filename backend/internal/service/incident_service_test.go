package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
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
		history, err := incSvc.StatusHistory(t.Context(), tenantID, inc.ID)
		require.NoError(t, err)
		require.Len(t, history, 1)
		assert.Equal(t, domain.PhaseNew, history[0].Phase)

		events, err := incSvc.Timeline(t.Context(), tenantID, inc.ID)
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

		events, err := incSvc.Timeline(t.Context(), tenantID, inc.ID)
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

		events, err := incSvc.Timeline(t.Context(), tenantID, inc.ID)
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
		events, err := incSvc.Timeline(t.Context(), tenantID, inc.ID)
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
		events, err := incSvc.Timeline(t.Context(), tenantID, inc.ID)
		require.NoError(t, err)
		assert.Len(t, events, 1, "still just the 'created' event")
	})

	t.Run("a direct forward jump logs both phase_changed and phase_skipped", func(t *testing.T) {
		require.NoError(t, incSvc.ChangePhase(t.Context(), tenantID, inc.ID, actorID, domain.PhaseContainment, nil))

		events, err := incSvc.Timeline(t.Context(), tenantID, inc.ID)
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

		events, err := incSvc.Timeline(t.Context(), tenantID, other.ID)
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
		err := incSvc.CorrectPhaseTimestamp(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, inc.OpenedAt, "")
		assert.ErrorContains(t, err, "reason is required")
	})

	t.Run("a valid correction is recorded with an event", func(t *testing.T) {
		require.NoError(t, incSvc.CorrectPhaseTimestamp(t.Context(), tenantID, inc.ID, actorID, domain.PhaseNew, inc.OpenedAt, "backdated per SOC log"))
		events, err := incSvc.Timeline(t.Context(), tenantID, inc.ID)
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
		c, err := incSvc.AddComment(t.Context(), tenantID, inc.ID, actorID, "Analyst", "investigating now", nil)
		require.NoError(t, err)
		assert.Equal(t, "investigating now", c.Body)

		comments, err := incSvc.Comments(t.Context(), tenantID, inc.ID)
		require.NoError(t, err)
		require.Len(t, comments, 1)
	})

	t.Run("alert linking", func(t *testing.T) {
		alert, _, err := alertSvc.Ingest(t.Context(), tenantID, testutil.NewWebhookEndpoint(t, tenantID), domain.Alert{Title: "a", Source: "s", Severity: domain.SeverityLow, Payload: testPayload}, nil, 0)
		require.NoError(t, err)

		require.NoError(t, incSvc.LinkAlert(t.Context(), tenantID, inc.ID, alert.ID, actorID))
		linked, err := incSvc.LinkedAlerts(t.Context(), tenantID, inc.ID)
		require.NoError(t, err)
		require.Len(t, linked, 1)
		assert.Equal(t, alert.ID, linked[0].ID)

		require.NoError(t, incSvc.UnlinkAlert(t.Context(), tenantID, inc.ID, alert.ID, actorID))
		linked, err = incSvc.LinkedAlerts(t.Context(), tenantID, inc.ID)
		require.NoError(t, err)
		assert.Empty(t, linked)
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
