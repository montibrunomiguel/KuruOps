package repository_test

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
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

	imageURL := "https://cdn.example.com/screenshot.png"
	require.NoError(t, repo.Close(t.Context(), tx, a.ID, domain.CloseAlertInput{
		Classification: domain.ClassificationFalsePositive,
		Comment:        "Confirmed benign",
		ImageURL:       &imageURL,
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
	imageURL := "https://example.com/evidence.png"
	c := &domain.AlertComment{
		AlertID: a.ID, TenantID: tenantID, AuthorID: authorID,
		AuthorName: "Diego Costa", Body: "escalating to IR", ImageURL: &imageURL,
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
		require.NotNil(t, comments[0].ImageURL)
		assert.Equal(t, imageURL, *comments[0].ImageURL)
	})
}
