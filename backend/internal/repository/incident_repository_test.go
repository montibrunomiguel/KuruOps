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

func newTestIncident(tenantID uuid.UUID, severity domain.Severity, priority domain.IncidentPriority, tags []string) *domain.Incident {
	if tags == nil {
		tags = []string{}
	}
	return &domain.Incident{
		TenantID: tenantID, Title: "Ransomware suspected", Description: "desc",
		Severity: severity, Priority: priority, Phase: domain.PhaseNew, Tags: tags,
	}
}

func TestIncidentRepository_InsertGet(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityCritical, domain.PriorityP1, []string{"ransomware"})
	require.NoError(t, repo.Insert(t.Context(), tx, inc))
	require.NotEqual(t, [16]byte{}, inc.ID)
	assert.Equal(t, domain.PhaseNew, inc.Phase)
	assert.False(t, inc.OpenedAt.IsZero())

	got, err := repo.Get(t.Context(), tx, inc.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Ransomware suspected", got.Title)
	assert.Equal(t, []string{"ransomware"}, got.Tags)

	t.Run("get unknown id returns nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

// TestIncidentRepository_Assignees guards the batch-loaded assignee join --
// see domain.Incident.Assignees's doc comment for why this is a live join
// (via AssigneesForIncidents) rather than denormalized at write time.
func TestIncidentRepository_Assignees(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	analystA := testutil.NewUser(t, tenantID, "analyst", nil)
	analystB := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	withAssignees := newTestIncident(tenantID, domain.SeverityCritical, domain.PriorityP1, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, withAssignees))
	require.NoError(t, repo.SetAssignees(t.Context(), tx, withAssignees.ID, tenantID, []uuid.UUID{analystA, analystB}))

	withoutAssignees := newTestIncident(tenantID, domain.SeverityLow, domain.PriorityP4, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, withoutAssignees))

	t.Run("Get resolves the current assignees", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, withAssignees.ID)
		require.NoError(t, err)
		require.Len(t, got.Assignees, 2)

		got, err = repo.Get(t.Context(), tx, withoutAssignees.ID)
		require.NoError(t, err)
		assert.Empty(t, got.Assignees)
		// Must be [], not nil -- a nil slice serializes to JSON `null`
		// instead of `[]`, which breaks any frontend code that calls
		// .map()/.length on it unconditionally (see AssigneesPanel).
		assert.NotNil(t, got.Assignees)
	})

	t.Run("List batch-resolves assignees too", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{})
		require.NoError(t, err)
		byID := map[uuid.UUID]*domain.Incident{}
		for i := range list {
			byID[list[i].ID] = &list[i]
		}
		require.Len(t, byID[withAssignees.ID].Assignees, 2)
		assert.Empty(t, byID[withoutAssignees.ID].Assignees)
		assert.NotNil(t, byID[withoutAssignees.ID].Assignees)
	})

	t.Run("SetAssignees replaces the set, not adds to it", func(t *testing.T) {
		require.NoError(t, repo.SetAssignees(t.Context(), tx, withAssignees.ID, tenantID, []uuid.UUID{analystA}))
		got, err := repo.Get(t.Context(), tx, withAssignees.ID)
		require.NoError(t, err)
		require.Len(t, got.Assignees, 1)
		assert.Equal(t, analystA, got.Assignees[0].ID)
	})

	t.Run("SetAssignees with an empty set clears every assignee", func(t *testing.T) {
		require.NoError(t, repo.SetAssignees(t.Context(), tx, withAssignees.ID, tenantID, nil))
		got, err := repo.Get(t.Context(), tx, withAssignees.ID)
		require.NoError(t, err)
		assert.Empty(t, got.Assignees)
	})
}

func TestIncidentRepository_List_Filters(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	p1 := newTestIncident(tenantID, domain.SeverityCritical, domain.PriorityP1, []string{"ransomware"})
	require.NoError(t, repo.Insert(t.Context(), tx, p1))

	p3 := newTestIncident(tenantID, domain.SeverityLow, domain.PriorityP3, []string{"phishing"})
	require.NoError(t, repo.Insert(t.Context(), tx, p3))

	t.Run("no filter returns everything", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{})
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("filter by priority", func(t *testing.T) {
		priority := domain.PriorityP1
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{Priority: &priority})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, p1.ID, list[0].ID)
	})

	t.Run("filter by severity", func(t *testing.T) {
		sev := domain.SeverityLow
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{Severity: &sev})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, p3.ID, list[0].ID)
	})

	t.Run("filter by phase", func(t *testing.T) {
		phase := domain.PhaseNew
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{Phase: &phase})
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("filter by tag", func(t *testing.T) {
		tag := "phishing"
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{Tag: &tag})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, p3.ID, list[0].ID)
	})

	t.Run("filter by AllowedTags", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{AllowedTags: []string{"ransomware"}})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, p1.ID, list[0].ID)
	})

	// Same reasoning as AlertRepository's combined-filter regression test:
	// every subtest above applies exactly one filter, which wouldn't catch a
	// positional-arg bug in the where-clause builder that only surfaces once
	// 2+ filters stack in the same call.
	t.Run("combined filters (severity + priority + phase + tag) narrow to the one incident matching all four", func(t *testing.T) {
		sev := domain.SeverityCritical
		priority := domain.PriorityP1
		phase := domain.PhaseNew
		tag := "ransomware"
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{
			Severity: &sev, Priority: &priority, Phase: &phase, Tag: &tag,
		})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, p1.ID, list[0].ID)
	})

	t.Run("combined filters where one condition matches nothing returns empty, not a partial match", func(t *testing.T) {
		sev := domain.SeverityCritical // p1 is critical...
		priority := domain.PriorityP3  // ...but not p3
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{Severity: &sev, Priority: &priority})
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("filter by SLABreached", func(t *testing.T) {
		breached := true
		list, err := repo.List(t.Context(), tx, repository.ListIncidentsFilter{SLABreached: &breached})
		require.NoError(t, err)
		assert.Empty(t, list, "neither fixture incident has breached its SLA")
	})
}

// TestIncidentRepository_Count is the regression test for real page-number
// pagination: Count must apply the same filters as List but ignore
// Limit/Offset entirely, so a caller can compute total pages independent of
// which page it's currently viewing.
func TestIncidentRepository_Count(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.Insert(t.Context(), tx, newTestIncident(tenantID, domain.SeverityCritical, domain.PriorityP1, []string{"ransomware"})))
	require.NoError(t, repo.Insert(t.Context(), tx, newTestIncident(tenantID, domain.SeverityLow, domain.PriorityP3, []string{"phishing"})))

	t.Run("no filter counts everything", func(t *testing.T) {
		count, err := repo.Count(t.Context(), tx, repository.ListIncidentsFilter{})
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})

	t.Run("count matches filtered list length, ignoring limit/offset", func(t *testing.T) {
		priority := domain.PriorityP1
		count, err := repo.Count(t.Context(), tx, repository.ListIncidentsFilter{Priority: &priority, Limit: 1, Offset: 0})
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("count reflects SLABreached filter", func(t *testing.T) {
		breached := true
		count, err := repo.Count(t.Context(), tx, repository.ListIncidentsFilter{SLABreached: &breached})
		require.NoError(t, err)
		assert.Equal(t, 0, count, "neither fixture incident has breached its SLA")
	})
}

func TestIncidentRepository_UpdatePhase(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityHigh, domain.PriorityP2, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, inc))

	t.Run("moving to post_incident alone does NOT stamp closed_at -- only MarkClosed does", func(t *testing.T) {
		require.NoError(t, repo.UpdatePhase(t.Context(), tx, inc.ID, domain.PhasePostIncident))
		got, err := repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.PhasePostIncident, got.Phase)
		assert.Nil(t, got.ClosedAt, "reaching post_incident via the phase tracker must not silently close the incident")
	})

	t.Run("MarkClosed stamps closed_at while in post_incident", func(t *testing.T) {
		require.NoError(t, repo.MarkClosed(t.Context(), tx, inc.ID))
		got, err := repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.NotNil(t, got.ClosedAt)
	})

	t.Run("MarkClosed is idempotent -- a second call keeps the original timestamp", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		require.NotNil(t, got.ClosedAt)
		firstClosedAt := *got.ClosedAt

		require.NoError(t, repo.MarkClosed(t.Context(), tx, inc.ID))
		got, err = repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.True(t, firstClosedAt.Equal(*got.ClosedAt), "a second MarkClosed must not bump the original close time")
	})

	t.Run("moving away from post_incident clears closed_at (reopen)", func(t *testing.T) {
		require.NoError(t, repo.UpdatePhase(t.Context(), tx, inc.ID, domain.PhaseContainment))
		got, err := repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.PhaseContainment, got.Phase)
		assert.Nil(t, got.ClosedAt, "reopening must clear the stale close time")
	})

	t.Run("re-reaching post_incident after a reopen does not resurrect the old close time", func(t *testing.T) {
		require.NoError(t, repo.UpdatePhase(t.Context(), tx, inc.ID, domain.PhasePostIncident))
		got, err := repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.Nil(t, got.ClosedAt, "must require an explicit MarkClosed again, not just re-reaching the phase")
	})
}

func TestIncidentRepository_SetSeverityAndPriorityAndDescription(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityLow, domain.PriorityP4, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, inc))
	assert.Nil(t, inc.SLADueAt, "no policy is passed to Insert in this fixture")

	dueAt := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	require.NoError(t, repo.SetSeverityAndPriority(t.Context(), tx, inc.ID, domain.SeverityCritical, domain.PriorityP1, &dueAt))
	require.NoError(t, repo.UpdateDescription(t.Context(), tx, inc.ID, "updated description"))

	got, err := repo.Get(t.Context(), tx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.SeverityCritical, got.Severity)
	assert.Equal(t, domain.PriorityP1, got.Priority)
	assert.Equal(t, "updated description", got.Description)
	require.NotNil(t, got.SLADueAt)
	assert.WithinDuration(t, dueAt, *got.SLADueAt, time.Second)

	t.Run("passing nil clears sla_due_at", func(t *testing.T) {
		require.NoError(t, repo.SetSeverityAndPriority(t.Context(), tx, inc.ID, domain.SeverityCritical, domain.PriorityP1, nil))
		got, err := repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.Nil(t, got.SLADueAt)
	})
}

func TestIncidentRepository_UpdateTags(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityLow, domain.PriorityP4, []string{"old"})
	require.NoError(t, repo.Insert(t.Context(), tx, inc))

	require.NoError(t, repo.UpdateTags(t.Context(), tx, inc.ID, []string{"new-a", "new-b"}))
	got, err := repo.Get(t.Context(), tx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"new-a", "new-b"}, got.Tags)
}

func TestIncidentRepository_StatusHistory(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	correctedBy := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityHigh, domain.PriorityP2, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, inc))

	inserted, err := repo.RecordPhaseEntered(t.Context(), tx, inc.ID, tenantID, domain.PhaseDetectionAnalysis)
	require.NoError(t, err)
	assert.True(t, inserted)

	t.Run("re-recording the same phase is a no-op", func(t *testing.T) {
		inserted, err := repo.RecordPhaseEntered(t.Context(), tx, inc.ID, tenantID, domain.PhaseDetectionAnalysis)
		require.NoError(t, err)
		assert.False(t, inserted, "unique index on (incident_id, phase) means a revisit doesn't duplicate")
	})

	history, err := repo.ListStatusHistory(t.Context(), tx, inc.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, domain.PhaseDetectionAnalysis, history[0].Phase)
	assert.Nil(t, history[0].CorrectedEnteredAt)

	t.Run("correct phase timestamp fills the correction fields without touching entered_at", func(t *testing.T) {
		correctedTime := time.Now().Add(-2 * time.Hour)
		require.NoError(t, repo.CorrectPhaseTimestamp(t.Context(), tx, inc.ID, domain.PhaseDetectionAnalysis, correctedTime, correctedBy, "analyst backdated the real detection time"))

		history, err := repo.ListStatusHistory(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		require.Len(t, history, 1)
		require.NotNil(t, history[0].CorrectedEnteredAt)
		assert.WithinDuration(t, correctedTime, *history[0].CorrectedEnteredAt, time.Second)
		require.NotNil(t, history[0].CorrectedBy)
		assert.Equal(t, correctedBy, *history[0].CorrectedBy)
		require.NotNil(t, history[0].CorrectionReason)
		assert.Equal(t, "analyst backdated the real detection time", *history[0].CorrectionReason)
		assert.Equal(t, correctedTime.Unix(), history[0].EffectiveEnteredAt().Unix(), "EffectiveEnteredAt must prefer the correction")
	})
}

func TestIncidentRepository_Events(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityHigh, domain.PriorityP2, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, inc))

	e := &domain.IncidentEvent{
		IncidentID: inc.ID, TenantID: tenantID, EventType: domain.IncidentEventPhaseChanged,
		ActorType: domain.ActorUser, Data: json.RawMessage(`{"to":"containment"}`),
	}
	require.NoError(t, repo.InsertEvent(t.Context(), tx, e))
	require.NotZero(t, e.ID)

	events, err := repo.ListEvents(t.Context(), tx, inc.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, domain.IncidentEventPhaseChanged, events[0].EventType)
}

func TestIncidentRepository_Comments(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityHigh, domain.PriorityP2, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, inc))

	c := &domain.IncidentComment{IncidentID: inc.ID, TenantID: tenantID, AuthorID: userID, Body: "Investigating now"}
	require.NoError(t, repo.InsertComment(t.Context(), tx, c))
	require.NotEqual(t, [16]byte{}, c.ID)

	comments, err := repo.ListComments(t.Context(), tx, inc.ID)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	assert.Equal(t, "Investigating now", comments[0].Body)
}

func TestIncidentRepository_AlertLinks(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	incRepo := repository.NewIncidentRepository()
	alertRepo := repository.NewAlertRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityHigh, domain.PriorityP2, nil)
	require.NoError(t, incRepo.Insert(t.Context(), tx, inc))
	alert := newTestAlert(tenantID, domain.SeverityHigh, domain.AlertStatusOpen, nil)
	require.NoError(t, alertRepo.Insert(t.Context(), tx, alert))

	require.NoError(t, incRepo.LinkAlert(t.Context(), tx, inc.ID, alert.ID, tenantID))

	t.Run("linking twice is a no-op, not a conflict error", func(t *testing.T) {
		require.NoError(t, incRepo.LinkAlert(t.Context(), tx, inc.ID, alert.ID, tenantID))
	})

	linked, err := incRepo.ListLinkedAlerts(t.Context(), tx, inc.ID)
	require.NoError(t, err)
	require.Len(t, linked, 1)
	assert.Equal(t, alert.ID, linked[0].ID)

	t.Run("unlink removes it", func(t *testing.T) {
		require.NoError(t, incRepo.UnlinkAlert(t.Context(), tx, inc.ID, alert.ID))
		linked, err := incRepo.ListLinkedAlerts(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.Empty(t, linked)
	})
}

func TestIncidentRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewIncidentRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	inc := newTestIncident(tenantA, domain.SeverityHigh, domain.PriorityP1, nil)
	require.NoError(t, repo.Insert(t.Context(), txA, inc))

	txB := testutil.BeginTx(t, pool, tenantB)
	list, err := repo.List(t.Context(), txB, repository.ListIncidentsFilter{})
	require.NoError(t, err)
	assert.Empty(t, list, "RLS must prevent tenant B from seeing tenant A's incidents")
}

func TestIncidentRepository_IncidentAssignees_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	analystA := testutil.NewUser(t, tenantA, "analyst", nil)
	repo := repository.NewIncidentRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	inc := newTestIncident(tenantA, domain.SeverityHigh, domain.PriorityP1, nil)
	require.NoError(t, repo.Insert(t.Context(), txA, inc))
	require.NoError(t, repo.SetAssignees(t.Context(), txA, inc.ID, tenantA, []uuid.UUID{analystA}))

	txB := testutil.BeginTx(t, pool, tenantB)
	assignees, err := repo.AssigneesForIncidents(t.Context(), txB, []uuid.UUID{inc.ID})
	require.NoError(t, err)
	assert.Empty(t, assignees[inc.ID], "RLS must prevent tenant B from seeing tenant A's incident_assignees rows")
}

// TestIncidentRepository_Roles guards SetRole's replace-the-set semantics
// (additive to, and independent of, incident_assignees -- see
// domain.Incident.Roles's doc comment) and the single-assignee
// (Commander/Technical Lead) DB constraint from
// db/migrations/0030_incident_role_assignments.
func TestIncidentRepository_Roles(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	analystA := testutil.NewUser(t, tenantID, "analyst", nil)
	analystB := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewIncidentRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	inc := newTestIncident(tenantID, domain.SeverityCritical, domain.PriorityP1, nil)
	require.NoError(t, repo.Insert(t.Context(), tx, inc))

	t.Run("no roles assigned initially -- empty, not nil", func(t *testing.T) {
		roles, err := repo.RolesForIncident(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.Empty(t, roles)
		assert.NotNil(t, roles)

		got, err := repo.Get(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		assert.NotNil(t, got.Roles)
	})

	t.Run("multi-assignee role accepts more than one person", func(t *testing.T) {
		require.NoError(t, repo.SetRole(t.Context(), tx, inc.ID, tenantID, domain.RoleIncidentHandler, []uuid.UUID{analystA, analystB}))
		roles, err := repo.RolesForIncident(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		require.Len(t, roles, 2)
		for _, r := range roles {
			assert.Equal(t, domain.RoleIncidentHandler, r.Role)
		}
	})

	t.Run("SetRole replaces the whole set for that role", func(t *testing.T) {
		require.NoError(t, repo.SetRole(t.Context(), tx, inc.ID, tenantID, domain.RoleIncidentHandler, []uuid.UUID{analystA}))
		roles, err := repo.RolesForIncident(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		require.Len(t, roles, 1)
		assert.Equal(t, analystA, roles[0].User.ID)
	})

	t.Run("single-assignee role accepts one person", func(t *testing.T) {
		require.NoError(t, repo.SetRole(t.Context(), tx, inc.ID, tenantID, domain.RoleCommander, []uuid.UUID{analystA}))
		roles, err := repo.RolesForIncident(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		commanders := 0
		for _, r := range roles {
			if r.Role == domain.RoleCommander {
				commanders++
			}
		}
		assert.Equal(t, 1, commanders)
	})

	t.Run("clearing a role with an empty set", func(t *testing.T) {
		require.NoError(t, repo.SetRole(t.Context(), tx, inc.ID, tenantID, domain.RoleIncidentHandler, nil))
		roles, err := repo.RolesForIncident(t.Context(), tx, inc.ID)
		require.NoError(t, err)
		for _, r := range roles {
			assert.NotEqual(t, domain.RoleIncidentHandler, r.Role)
		}
	})

	// Last: a failed statement poisons the rest of the enclosing Postgres
	// transaction (aborts it until rollback), so nothing else in this tx
	// can run after this subtest.
	t.Run("the DB rejects two Commanders in one SetRole call", func(t *testing.T) {
		err := repo.SetRole(t.Context(), tx, inc.ID, tenantID, domain.RoleCommander, []uuid.UUID{analystA, analystB})
		assert.Error(t, err, "the partial unique index on (incident_id) where role='commander' must reject a second row")
	})
}

func TestIncidentRepository_Roles_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	analystA := testutil.NewUser(t, tenantA, "analyst", nil)
	repo := repository.NewIncidentRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	inc := newTestIncident(tenantA, domain.SeverityHigh, domain.PriorityP1, nil)
	require.NoError(t, repo.Insert(t.Context(), txA, inc))
	require.NoError(t, repo.SetRole(t.Context(), txA, inc.ID, tenantA, domain.RoleCommander, []uuid.UUID{analystA}))

	txB := testutil.BeginTx(t, pool, tenantB)
	roles, err := repo.RolesForIncident(t.Context(), txB, inc.ID)
	require.NoError(t, err)
	assert.Empty(t, roles, "RLS must prevent tenant B from seeing tenant A's incident_role_assignments rows")
}
