package repository_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestMFAPendingTokenRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "admin", nil)
	repo := repository.NewMFAPendingTokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("unknown hash finds nothing, not an error", func(t *testing.T) {
		got, err := repo.GetByHash(t.Context(), tx, "no-such-hash")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	token := &domain.MFAPendingToken{
		TenantID: tenantID, UserID: userID, TokenHash: "hash-1",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	require.NoError(t, repo.Insert(t.Context(), tx, token))
	require.NotEqual(t, token.ID.String(), "")

	t.Run("finds the matching row without consuming it -- a caller can look it up more than once", func(t *testing.T) {
		got, err := repo.GetByHash(t.Context(), tx, "hash-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, userID, got.UserID)
		assert.Nil(t, got.ConsumedAt)

		got, err = repo.GetByHash(t.Context(), tx, "hash-1")
		require.NoError(t, err)
		require.NotNil(t, got, "GetByHash alone must not consume it")
	})

	t.Run("MarkConsumed makes it unfindable afterward -- single use once actually consumed", func(t *testing.T) {
		require.NoError(t, repo.MarkConsumed(t.Context(), tx, token.ID))

		got, err := repo.GetByHash(t.Context(), tx, "hash-1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("an expired token is never findable", func(t *testing.T) {
		expired := &domain.MFAPendingToken{
			TenantID: tenantID, UserID: userID, TokenHash: "hash-expired",
			ExpiresAt: time.Now().Add(-time.Minute),
		}
		require.NoError(t, repo.Insert(t.Context(), tx, expired))

		got, err := repo.GetByHash(t.Context(), tx, "hash-expired")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestMFAPendingTokenRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantA, "admin", nil)
	repo := repository.NewMFAPendingTokenRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Insert(t.Context(), txA, &domain.MFAPendingToken{
		TenantID: tenantA, UserID: userA, TokenHash: "hash-a", ExpiresAt: time.Now().Add(10 * time.Minute),
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.GetByHash(t.Context(), txB, "hash-a")
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from reading tenant A's mfa pending token")
}
