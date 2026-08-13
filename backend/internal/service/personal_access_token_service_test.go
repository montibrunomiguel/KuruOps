package service_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newPATService(t *testing.T) *service.PersonalAccessTokenService {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	return service.NewPersonalAccessTokenService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewPersonalAccessTokenRepository())
}

func TestPersonalAccessTokenService_Create(t *testing.T) {
	svc := newPATService(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("rejects a missing name", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, userID, "", nil)
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("no expiry given -- never expires (opposite default from webhooks)", func(t *testing.T) {
		result, err := svc.Create(t.Context(), tenantID, userID, "CI script", nil)
		require.NoError(t, err)
		assert.True(t, len(result.Plaintext) > len("pat_"), "plaintext is prefixed and non-trivial")
		assert.Contains(t, result.Plaintext, "pat_")
		assert.Nil(t, result.Token.ExpiresAt)
		assert.Len(t, result.Token.TokenLast4, 4)
		assert.Equal(t, result.Plaintext[len(result.Plaintext)-4:], result.Token.TokenLast4)
	})

	t.Run("an explicit expiry is honored", func(t *testing.T) {
		days := 30
		result, err := svc.Create(t.Context(), tenantID, userID, "Integration X", &days)
		require.NoError(t, err)
		require.NotNil(t, result.Token.ExpiresAt)
		assert.WithinDuration(t, time.Now().AddDate(0, 0, 30), *result.Token.ExpiresAt, time.Minute)
	})

	t.Run("list shows both tokens without ever exposing the plaintext", func(t *testing.T) {
		list, err := svc.List(t.Context(), tenantID, userID)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})
}

func TestPersonalAccessTokenService_Revoke(t *testing.T) {
	svc := newPATService(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	otherUserID := testutil.NewUser(t, tenantID, "analyst", nil)

	result, err := svc.Create(t.Context(), tenantID, userID, "CI script", nil)
	require.NoError(t, err)

	t.Run("another user's revoke attempt errors and leaves the token untouched", func(t *testing.T) {
		// Revoke's WHERE clause (id AND user_id) is the authorization check
		// itself -- a mismatched user_id matches zero rows, which the
		// repository surfaces as an error rather than a silent no-op (see
		// PersonalAccessTokenRepository.Revoke's doc comment).
		assert.ErrorContains(t, svc.Revoke(t.Context(), tenantID, otherUserID, result.Token.ID), "not found")

		list, err := svc.List(t.Context(), tenantID, userID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Nil(t, list[0].RevokedAt)
	})

	t.Run("the owning user can revoke it", func(t *testing.T) {
		require.NoError(t, svc.Revoke(t.Context(), tenantID, userID, result.Token.ID))

		list, err := svc.List(t.Context(), tenantID, userID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.NotNil(t, list[0].RevokedAt)
	})
}

func TestPersonalAccessTokenService_Resolve(t *testing.T) {
	svc := newPATService(t)
	pool := testutil.RequireTestDB(t)

	// Resolve looks up "the" tenant the same way login does (see
	// TenantRepository.GetDefault's doc comment -- single-instance
	// software, exactly one tenant in production) rather than taking one
	// as an argument, so this test's fixtures have to live under whichever
	// tenant GetDefault actually resolves (the oldest row in `tenants`),
	// not a fresh testutil.NewTenant() that GetDefault would never pick.
	defaultTenant, err := repository.NewTenantRepository().GetDefault(t.Context(), pool)
	require.NoError(t, err)
	require.NotNil(t, defaultTenant, "the seed migration guarantees at least one tenant exists")
	tenantID := defaultTenant.ID
	userID := testutil.NewUser(t, tenantID, "admin", nil)

	result, err := svc.Create(t.Context(), tenantID, userID, "CI script", nil)
	require.NoError(t, err)

	t.Run("a valid token resolves the owning user's current permissions", func(t *testing.T) {
		gotTenant, gotUser, isAdmin, _, _, ok, err := svc.Resolve(t.Context(), result.Plaintext)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, tenantID, gotTenant)
		assert.Equal(t, userID, gotUser)
		assert.True(t, isAdmin)
	})

	t.Run("garbage token -- ok=false, not an error", func(t *testing.T) {
		_, _, _, _, _, ok, err := svc.Resolve(t.Context(), "pat_not-a-real-token")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("a revoked token stops resolving", func(t *testing.T) {
		require.NoError(t, svc.Revoke(t.Context(), tenantID, userID, result.Token.ID))
		_, _, _, _, _, ok, err := svc.Resolve(t.Context(), result.Plaintext)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("an expired token stops resolving", func(t *testing.T) {
		// Create's own expiresInDays param treats 0-or-negative as "never
		// expires" (see resolvePATExpiry), so there's no way to reach an
		// already-expired token through the public API -- insert one
		// directly instead, mirroring Create's own hash/last-4 logic.
		// Unique per run -- Resolve looks it up via its own separate,
		// committed transaction (WithTenant), so this fixture has to be a
		// real commit too (not a rollback-at-cleanup tx like most fixtures
		// in this codebase use), which means a fixed literal here would
		// collide with the leftover row from a previous run against the
		// same local Postgres.
		plaintext := "pat_expired-test-token-" + uuid.NewString()
		sum := sha256.Sum256([]byte(plaintext))
		past := time.Now().Add(-time.Hour)
		expiredPAT := &domain.PersonalAccessToken{
			TenantID: tenantID, UserID: userID, Name: "Expired",
			TokenHash: hex.EncodeToString(sum[:]), TokenLast4: plaintext[len(plaintext)-4:],
			ExpiresAt: &past,
		}
		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return repository.NewPersonalAccessTokenRepository().Insert(t.Context(), tx, expiredPAT)
		}))

		_, _, _, _, _, ok, err := svc.Resolve(t.Context(), plaintext)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("a deactivated user's token stops resolving", func(t *testing.T) {
		userSvc := service.NewUserService(testutil.RequireTestDB(t), repository.NewUserRepository())
		deactivated, err := svc.Create(t.Context(), tenantID, userID, "About to be deactivated", nil)
		require.NoError(t, err)
		require.NoError(t, userSvc.SetActive(t.Context(), tenantID, userID, false))

		_, _, _, _, _, ok, err := svc.Resolve(t.Context(), deactivated.Plaintext)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}
