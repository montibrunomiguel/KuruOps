package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestRetentionConfigRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewRetentionConfigRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no config yet returns nil, not an error", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	require.NoError(t, repo.Upsert(t.Context(), tx, &domain.RetentionConfig{
		TenantID: tenantID, AlertRetentionMonths: 12, IncidentRetentionMonths: 24,
	}))

	t.Run("get after insert reflects the saved values and Configured=true", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 12, got.AlertRetentionMonths)
		assert.Equal(t, 24, got.IncidentRetentionMonths)
		assert.True(t, got.Configured)
		require.NotNil(t, got.UpdatedAt)
	})

	t.Run("upsert replaces the row", func(t *testing.T) {
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.RetentionConfig{
			TenantID: tenantID, AlertRetentionMonths: 0, IncidentRetentionMonths: 6,
		}))
		got, err := repo.Get(t.Context(), tx)
		require.NoError(t, err)
		assert.Equal(t, 0, got.AlertRetentionMonths)
		assert.Equal(t, 6, got.IncidentRetentionMonths)
	})
}

func TestRetentionConfigRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewRetentionConfigRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Upsert(t.Context(), txA, &domain.RetentionConfig{
		TenantID: tenantA, AlertRetentionMonths: 3, IncidentRetentionMonths: 3,
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.Get(t.Context(), txB)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's retention config")
}
