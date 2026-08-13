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

func TestPersonalAccessTokenRepository_InsertGetRevoke(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewPersonalAccessTokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	pat := &domain.PersonalAccessToken{TenantID: tenantID, UserID: userID, Name: "CI script", TokenHash: "hash-1", TokenLast4: "abcd"}
	require.NoError(t, repo.Insert(t.Context(), tx, pat))
	require.NotEqual(t, [16]byte{}, pat.ID)

	got, err := repo.GetByHash(t.Context(), tx, "hash-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, userID, got.UserID)
	assert.Equal(t, "CI script", got.Name)
	assert.Nil(t, got.RevokedAt)
	assert.Nil(t, got.LastUsedAt)

	unknown, err := repo.GetByHash(t.Context(), tx, "no-such-hash")
	require.NoError(t, err)
	assert.Nil(t, unknown, "an unknown hash is nil, not an error")

	require.NoError(t, repo.StampLastUsed(t.Context(), tx, pat.ID))
	got, err = repo.GetByHash(t.Context(), tx, "hash-1")
	require.NoError(t, err)
	assert.NotNil(t, got.LastUsedAt)

	require.NoError(t, repo.Revoke(t.Context(), tx, pat.ID, userID))
	got, err = repo.GetByHash(t.Context(), tx, "hash-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.NotNil(t, got.RevokedAt)
}

func TestPersonalAccessTokenRepository_RevokeWrongUser(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	owner := testutil.NewUser(t, tenantID, "analyst", nil)
	someoneElse := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewPersonalAccessTokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	pat := &domain.PersonalAccessToken{TenantID: tenantID, UserID: owner, Name: "owner's token", TokenHash: "owner-hash", TokenLast4: "wxyz"}
	require.NoError(t, repo.Insert(t.Context(), tx, pat))

	err := repo.Revoke(t.Context(), tx, pat.ID, someoneElse)
	assert.Error(t, err, "revoking a token that belongs to a different user must fail")

	got, err := repo.GetByHash(t.Context(), tx, "owner-hash")
	require.NoError(t, err)
	assert.Nil(t, got.RevokedAt, "the owner's token must still be live")
}

func TestPersonalAccessTokenRepository_ListForUser(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantID, "analyst", nil)
	userB := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewPersonalAccessTokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.Insert(t.Context(), tx, &domain.PersonalAccessToken{TenantID: tenantID, UserID: userA, Name: "a1", TokenHash: "a1-hash", TokenLast4: "aaaa"}))
	require.NoError(t, repo.Insert(t.Context(), tx, &domain.PersonalAccessToken{TenantID: tenantID, UserID: userA, Name: "a2", TokenHash: "a2-hash", TokenLast4: "bbbb"}))
	require.NoError(t, repo.Insert(t.Context(), tx, &domain.PersonalAccessToken{TenantID: tenantID, UserID: userB, Name: "b1", TokenHash: "b1-hash", TokenLast4: "cccc"}))

	listA, err := repo.ListForUser(t.Context(), tx, userA)
	require.NoError(t, err)
	assert.Len(t, listA, 2, "must not include userB's token")
}

func TestPersonalAccessTokenRepository_ExpiresAt(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewPersonalAccessTokenRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	expires := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	pat := &domain.PersonalAccessToken{TenantID: tenantID, UserID: userID, Name: "expiring", TokenHash: "expiring-hash", TokenLast4: "eeee", ExpiresAt: &expires}
	require.NoError(t, repo.Insert(t.Context(), tx, pat))

	got, err := repo.GetByHash(t.Context(), tx, "expiring-hash")
	require.NoError(t, err)
	require.NotNil(t, got.ExpiresAt)
	assert.WithinDuration(t, expires, *got.ExpiresAt, time.Second)
}

func TestPersonalAccessTokenRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	userA := testutil.NewUser(t, tenantA, "analyst", nil)
	repo := repository.NewPersonalAccessTokenRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Insert(t.Context(), txA, &domain.PersonalAccessToken{TenantID: tenantA, UserID: userA, Name: "tenant a's token", TokenHash: "tenant-a-hash", TokenLast4: "ffff"}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.GetByHash(t.Context(), txB, "tenant-a-hash")
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from looking up tenant A's personal access token")
}
