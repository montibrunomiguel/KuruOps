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

func TestRefreshTokenRepository_InsertGetRevoke(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewRefreshTokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	rt := &domain.RefreshToken{TenantID: tenantID, UserID: userID, TokenHash: "hash-1", ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	require.NoError(t, repo.Insert(t.Context(), tx, rt))
	require.NotEqual(t, [16]byte{}, rt.ID)

	got, err := repo.GetByHash(t.Context(), tx, "hash-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, userID, got.UserID)
	assert.Nil(t, got.RevokedAt)

	unknown, err := repo.GetByHash(t.Context(), tx, "no-such-hash")
	require.NoError(t, err)
	assert.Nil(t, unknown, "an unknown hash is nil, not an error")

	require.NoError(t, repo.Revoke(t.Context(), tx, rt.ID))
	got, err = repo.GetByHash(t.Context(), tx, "hash-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.NotNil(t, got.RevokedAt)
}

func TestRefreshTokenRepository_RevokeAllForUser(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantID, "analyst", nil)
	userB := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewRefreshTokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	rtA1 := &domain.RefreshToken{TenantID: tenantID, UserID: userA, TokenHash: "a1", ExpiresAt: time.Now().Add(time.Hour)}
	rtA2 := &domain.RefreshToken{TenantID: tenantID, UserID: userA, TokenHash: "a2", ExpiresAt: time.Now().Add(time.Hour)}
	rtB := &domain.RefreshToken{TenantID: tenantID, UserID: userB, TokenHash: "b1", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, repo.Insert(t.Context(), tx, rtA1))
	require.NoError(t, repo.Insert(t.Context(), tx, rtA2))
	require.NoError(t, repo.Insert(t.Context(), tx, rtB))

	require.NoError(t, repo.RevokeAllForUser(t.Context(), tx, userA))

	a1, err := repo.GetByHash(t.Context(), tx, "a1")
	require.NoError(t, err)
	assert.NotNil(t, a1.RevokedAt)

	a2, err := repo.GetByHash(t.Context(), tx, "a2")
	require.NoError(t, err)
	assert.NotNil(t, a2.RevokedAt)

	b, err := repo.GetByHash(t.Context(), tx, "b1")
	require.NoError(t, err)
	assert.Nil(t, b.RevokedAt, "a different user's tokens must not be revoked")
}

func TestRefreshTokenRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantA, "analyst", nil)
	repo := repository.NewRefreshTokenRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Insert(t.Context(), txA, &domain.RefreshToken{TenantID: tenantA, UserID: userA, TokenHash: "tenant-a-hash", ExpiresAt: time.Now().Add(time.Hour)}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.GetByHash(t.Context(), txB, "tenant-a-hash")
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from looking up tenant A's refresh token")
}
