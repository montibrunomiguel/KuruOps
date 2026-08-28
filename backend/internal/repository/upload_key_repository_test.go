package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestUploadKeyRepository_InsertAndLookup(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewUploadKeyRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no row yet -- found is false, not an error", func(t *testing.T) {
		_, _, found, err := repo.Lookup(t.Context(), tx, tenantID, "Alert/2026/01/01/some-alert/"+uuid.NewString()+"_evidence.png")
		require.NoError(t, err)
		assert.False(t, found)
	})

	key := "Alert/2026/01/01/some-alert/" + uuid.NewString() + "_evidence.png"
	alertID := uuid.New()
	require.NoError(t, repo.Insert(t.Context(), tx, tenantID, key, "alert", alertID))

	t.Run("lookup after insert", func(t *testing.T) {
		contextType, contextID, found, err := repo.Lookup(t.Context(), tx, tenantID, key)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "alert", contextType)
		assert.Equal(t, alertID, contextID)
	})
}

func TestUploadKeyRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewUploadKeyRepository()

	key := "Alert/2026/01/01/some-alert/" + uuid.NewString() + "_evidence.png"
	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Insert(t.Context(), txA, tenantA, key, "alert", uuid.New()))

	txB := testutil.BeginTx(t, pool, tenantB)
	_, _, found, err := repo.Lookup(t.Context(), txB, tenantB, key)
	require.NoError(t, err)
	assert.False(t, found, "RLS must prevent tenant B from seeing tenant A's upload key")
}
