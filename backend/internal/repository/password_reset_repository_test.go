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

func TestPasswordResetRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewPasswordResetRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("unknown hash returns nil, not an error", func(t *testing.T) {
		got, err := repo.GetByHash(t.Context(), tx, "no-such-hash")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	token := &domain.PasswordResetToken{
		TenantID: tenantID, UserID: userID, TokenHash: "hash-1", ExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, repo.Insert(t.Context(), tx, token))
	require.NotEqual(t, token.ID.String(), "")

	t.Run("get after insert", func(t *testing.T) {
		got, err := repo.GetByHash(t.Context(), tx, "hash-1")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, userID, got.UserID)
		assert.Nil(t, got.UsedAt)
	})

	t.Run("MarkUsed sets used_at", func(t *testing.T) {
		require.NoError(t, repo.MarkUsed(t.Context(), tx, token.ID))
		got, err := repo.GetByHash(t.Context(), tx, "hash-1")
		require.NoError(t, err)
		require.NotNil(t, got.UsedAt)
	})

	t.Run("DeleteAllForUser removes every token for that user", func(t *testing.T) {
		require.NoError(t, repo.DeleteAllForUser(t.Context(), tx, userID))
		got, err := repo.GetByHash(t.Context(), tx, "hash-1")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestPasswordResetRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantA, "analyst", nil)
	repo := repository.NewPasswordResetRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Insert(t.Context(), txA, &domain.PasswordResetToken{
		TenantID: tenantA, UserID: userA, TokenHash: "hash-a", ExpiresAt: time.Now().Add(time.Hour),
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.GetByHash(t.Context(), txB, "hash-a")
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's reset token")
}
