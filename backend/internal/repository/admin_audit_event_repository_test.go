package repository_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestAdminAuditEventRepository_InsertAndList(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	repo := repository.NewAdminAuditEventRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	data, _ := json.Marshal(map[string]any{"from": nil, "to": "smtp.example.com"})
	event := &domain.AdminAuditEvent{
		TenantID: tenantID, Area: "smtp", Action: "save", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
	}
	require.NoError(t, repo.InsertEvent(t.Context(), tx, event))
	assert.NotZero(t, event.ID, "InsertEvent must populate the generated id")
	assert.False(t, event.CreatedAt.IsZero(), "InsertEvent must populate the generated created_at")

	t.Run("List returns the inserted event", func(t *testing.T) {
		events, err := repo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "smtp", events[0].Area)
		assert.Equal(t, "save", events[0].Action)
		assert.Equal(t, domain.ActorUser, events[0].ActorType)
		assert.Equal(t, actorID, events[0].ActorID)
		assert.JSONEq(t, `{"from":null,"to":"smtp.example.com"}`, string(events[0].Data))
	})
}

func TestAdminAuditEventRepository_List_NewestFirstWithKeysetPagination(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	repo := repository.NewAdminAuditEventRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	insert := func(area string) {
		require.NoError(t, repo.InsertEvent(t.Context(), tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: area, Action: "save", ActorType: domain.ActorUser, ActorID: actorID, Data: json.RawMessage(`{}`),
		}))
	}
	insert("webhooks")
	insert("retention")
	insert("smtp")

	t.Run("first page, newest first", func(t *testing.T) {
		page, err := repo.List(t.Context(), tx, nil, 2)
		require.NoError(t, err)
		require.Len(t, page, 2)
		assert.Equal(t, "smtp", page[0].Area, "most recently inserted must come first")
		assert.Equal(t, "retention", page[1].Area)
	})

	t.Run("second page picks up where the cursor left off", func(t *testing.T) {
		page, err := repo.List(t.Context(), tx, nil, 2)
		require.NoError(t, err)
		require.Len(t, page, 2)
		last := page[len(page)-1]

		next, err := repo.List(t.Context(), tx, &repository.AdminAuditEventCursor{CreatedAt: last.CreatedAt, ID: last.ID}, 2)
		require.NoError(t, err)
		require.Len(t, next, 1)
		assert.Equal(t, "webhooks", next[0].Area, "the oldest event must be the only one left after paging past the first two")
	})
}

func TestAdminAuditEventRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	actorA := testutil.NewUser(t, tenantA, "admin", nil)
	repo := repository.NewAdminAuditEventRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.InsertEvent(t.Context(), txA, &domain.AdminAuditEvent{
		TenantID: tenantA, Area: "webhooks", Action: "save", ActorType: domain.ActorUser, ActorID: actorA, Data: json.RawMessage(`{}`),
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	events, err := repo.List(t.Context(), txB, nil, 10)
	require.NoError(t, err)
	assert.Empty(t, events, "RLS must prevent tenant B from seeing tenant A's audit events")
}

// TestAdminAuditEventRepository_KeysetTiebreaksOnID proves the keyset
// cursor's ID tiebreaker actually matters, not just CreatedAt -- two rows
// inserted in the same statement (hence the same or near-identical
// timestamp) must still page deterministically without skipping or
// repeating either one.
func TestAdminAuditEventRepository_KeysetTiebreaksOnID(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	repo := repository.NewAdminAuditEventRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	// Two back-to-back InsertEvent calls, fast enough in practice to often
	// land within the same millisecond -- exactly the scenario this test
	// needs, without fighting a raw multi-row insert to force it.
	require.NoError(t, repo.InsertEvent(t.Context(), tx, &domain.AdminAuditEvent{
		TenantID: tenantID, Area: "first", Action: "save", ActorType: domain.ActorUser, ActorID: actorID, Data: json.RawMessage(`{}`),
	}))
	require.NoError(t, repo.InsertEvent(t.Context(), tx, &domain.AdminAuditEvent{
		TenantID: tenantID, Area: "second", Action: "save", ActorType: domain.ActorUser, ActorID: actorID, Data: json.RawMessage(`{}`),
	}))

	all, err := repo.List(t.Context(), tx, nil, 10)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "second", all[0].Area, "higher id must sort first among equal/near-equal timestamps")
	assert.Equal(t, "first", all[1].Area)
}
