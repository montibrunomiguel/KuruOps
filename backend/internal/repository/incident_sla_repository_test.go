package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestIncidentSLARepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewIncidentSLARepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("Lookup for an unconfigured pair returns nil, not an error", func(t *testing.T) {
		got, err := repo.Lookup(t.Context(), tx, domain.SeverityCritical, domain.PriorityP1)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	p := &domain.IncidentSLAPolicy{
		TenantID: tenantID, Severity: domain.SeverityCritical, Priority: domain.PriorityP1, DueWithinMinutes: 60,
	}
	require.NoError(t, repo.Upsert(t.Context(), tx, p))
	require.NotEqual(t, p.ID.String(), "")

	t.Run("Lookup after upsert", func(t *testing.T) {
		got, err := repo.Lookup(t.Context(), tx, domain.SeverityCritical, domain.PriorityP1)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 60, got.DueWithinMinutes)
	})

	t.Run("upserting the same pair again replaces due_within_minutes, keeps the same row", func(t *testing.T) {
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.IncidentSLAPolicy{
			TenantID: tenantID, Severity: domain.SeverityCritical, Priority: domain.PriorityP1, DueWithinMinutes: 30,
		}))
		got, err := repo.Lookup(t.Context(), tx, domain.SeverityCritical, domain.PriorityP1)
		require.NoError(t, err)
		assert.Equal(t, 30, got.DueWithinMinutes)
		assert.Equal(t, p.ID, got.ID, "upsert on the same pair must update the existing row, not insert a second one")
	})

	t.Run("List returns every configured policy", func(t *testing.T) {
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.IncidentSLAPolicy{
			TenantID: tenantID, Severity: domain.SeverityHigh, Priority: domain.PriorityP2, DueWithinMinutes: 240,
		}))
		policies, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		assert.Len(t, policies, 2)
	})

	t.Run("Delete removes a policy", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, p.ID))
		got, err := repo.Lookup(t.Context(), tx, domain.SeverityCritical, domain.PriorityP1)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestIncidentSLARepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewIncidentSLARepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Upsert(t.Context(), txA, &domain.IncidentSLAPolicy{
		TenantID: tenantA, Severity: domain.SeverityCritical, Priority: domain.PriorityP1, DueWithinMinutes: 60,
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.Lookup(t.Context(), txB, domain.SeverityCritical, domain.PriorityP1)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's SLA policy")
}
