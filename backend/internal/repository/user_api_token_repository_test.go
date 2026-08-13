package repository_test

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestUserAPITokenRepository_InsertListRevoke(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserAPITokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	tok := &domain.UserAPIToken{
		TenantID:   tenantID,
		UserID:     userID,
		Name:       "CI script",
		TokenHash:  "hash-1",
		TokenLast4: "ab12",
	}
	require.NoError(t, repo.Insert(t.Context(), tx, tok))
	require.NotEqual(t, [16]byte{}, tok.ID)
	assert.Nil(t, tok.RevokedAt)

	t.Run("list by user", func(t *testing.T) {
		list, err := repo.ListByUser(t.Context(), tx, userID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "CI script", list[0].Name)
	})

	t.Run("a different user sees none", func(t *testing.T) {
		otherUserID := testutil.NewUser(t, tenantID, "other", nil)
		list, err := repo.ListByUser(t.Context(), tx, otherUserID)
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("revoke stamps revoked_at, and is a no-op the second time", func(t *testing.T) {
		require.NoError(t, repo.Revoke(t.Context(), tx, tok.ID, userID))
		list, err := repo.ListByUser(t.Context(), tx, userID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.NotNil(t, list[0].RevokedAt)
		firstRevoke := *list[0].RevokedAt

		require.NoError(t, repo.Revoke(t.Context(), tx, tok.ID, userID))
		list2, err := repo.ListByUser(t.Context(), tx, userID)
		require.NoError(t, err)
		assert.Equal(t, firstRevoke, *list2[0].RevokedAt, "revoking an already-revoked token must not move the timestamp")
	})

	t.Run("revoke scoped to the wrong user is a no-op, not an error", func(t *testing.T) {
		tok2 := &domain.UserAPIToken{TenantID: tenantID, UserID: userID, Name: "second token", TokenHash: "hash-2", TokenLast4: "cd34"}
		require.NoError(t, repo.Insert(t.Context(), tx, tok2))

		otherUserID := testutil.NewUser(t, tenantID, "other2", nil)
		require.NoError(t, repo.Revoke(t.Context(), tx, tok2.ID, otherUserID))

		list, err := repo.ListByUser(t.Context(), tx, userID)
		require.NoError(t, err)
		var found *domain.UserAPIToken
		for i := range list {
			if list[i].ID == tok2.ID {
				found = &list[i]
			}
		}
		require.NotNil(t, found)
		assert.Nil(t, found.RevokedAt, "a revoke scoped to a different user id must not touch this token")
	})
}

func TestUserAPITokenRepository_ResolveToken(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserAPITokenRepository()

	// ResolveToken opens its own transaction against the pool (it runs
	// before app.tenant_id can be known), so the fixture insert must be
	// committed on a separate transaction -- same reasoning as
	// TestWebhookRepository_ResolveToken. The hash must be unique per run
	// since this row outlives the test.
	tokenHash := "resolve-hash-" + tenantID.String()
	var tok domain.UserAPIToken
	require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		tok = domain.UserAPIToken{TenantID: tenantID, UserID: userID, Name: "CI script", TokenHash: tokenHash, TokenLast4: "ef56"}
		return repo.Insert(t.Context(), tx, &tok)
	}))

	t.Run("resolves without app.tenant_id set, via the pool directly", func(t *testing.T) {
		got, err := repo.ResolveToken(t.Context(), pool, tokenHash)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, tok.ID, got.ID)
		assert.Equal(t, tenantID, got.TenantID)
		assert.Equal(t, userID, got.UserID)
	})

	t.Run("unknown token hash returns nil, not an error", func(t *testing.T) {
		got, err := repo.ResolveToken(t.Context(), pool, "no-such-hash")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
