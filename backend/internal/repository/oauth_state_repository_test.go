package repository_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestOAuthStateRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", nil)
	repo := repository.NewOAuthStateRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("unknown hash consumes nothing, not an error", func(t *testing.T) {
		got, err := repo.ConsumeByHash(t.Context(), tx, domain.OAuthProviderGDrive, "no-such-hash")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	state := &domain.OAuthState{
		TenantID: tenantID, UserID: userID, Provider: domain.OAuthProviderGDrive,
		TokenHash: "hash-1", Metadata: map[string]string{"folderId": "abc123"},
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	require.NoError(t, repo.Insert(t.Context(), tx, state))
	require.NotEqual(t, state.ID.String(), "")

	t.Run("wrong provider doesn't match, even with the right hash", func(t *testing.T) {
		got, err := repo.ConsumeByHash(t.Context(), tx, domain.OAuthProviderSlack, "hash-1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("consumes the matching row and round-trips its metadata", func(t *testing.T) {
		got, err := repo.ConsumeByHash(t.Context(), tx, domain.OAuthProviderGDrive, "hash-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, userID, got.UserID)
		assert.Equal(t, "abc123", got.Metadata["folderId"])
		assert.NotNil(t, got.ConsumedAt)
	})

	t.Run("a second consume of the same hash finds nothing -- single use", func(t *testing.T) {
		got, err := repo.ConsumeByHash(t.Context(), tx, domain.OAuthProviderGDrive, "hash-1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("an expired state is never consumable", func(t *testing.T) {
		expired := &domain.OAuthState{
			TenantID: tenantID, UserID: userID, Provider: domain.OAuthProviderGDrive,
			TokenHash: "hash-expired", Metadata: map[string]string{},
			ExpiresAt: time.Now().Add(-time.Minute),
		}
		require.NoError(t, repo.Insert(t.Context(), tx, expired))

		got, err := repo.ConsumeByHash(t.Context(), tx, domain.OAuthProviderGDrive, "hash-expired")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestOAuthStateRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantA, "admin", nil)
	repo := repository.NewOAuthStateRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Insert(t.Context(), txA, &domain.OAuthState{
		TenantID: tenantA, UserID: userA, Provider: domain.OAuthProviderGDrive,
		TokenHash: "hash-a", Metadata: map[string]string{}, ExpiresAt: time.Now().Add(10 * time.Minute),
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.ConsumeByHash(t.Context(), txB, domain.OAuthProviderGDrive, "hash-a")
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from consuming tenant A's oauth state")
}
