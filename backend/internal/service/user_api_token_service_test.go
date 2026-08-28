package service_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestUserAPITokenService_CreateListRevoke(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), repository.NewUserRepository())

	t.Run("Create defaults to a 90-day expiry and returns the plaintext once", func(t *testing.T) {
		result, err := svc.Create(t.Context(), tenantID, userID, "CI script", nil)
		require.NoError(t, err)
		assert.Contains(t, result.Plaintext, "pat_")
		require.NotNil(t, result.Token.ExpiresAt)
		assert.WithinDuration(t, time.Now().AddDate(0, 0, 90), *result.Token.ExpiresAt, time.Minute)
	})

	t.Run("Create with expiresInDays<=0 never expires", func(t *testing.T) {
		never := 0
		result, err := svc.Create(t.Context(), tenantID, userID, "Never expires", &never)
		require.NoError(t, err)
		assert.Nil(t, result.Token.ExpiresAt)
	})

	t.Run("List returns every token for the user", func(t *testing.T) {
		list, err := svc.List(t.Context(), tenantID, userID)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("Revoke then Resolve rejects it", func(t *testing.T) {
		result, err := svc.Create(t.Context(), tenantID, userID, "to be revoked", nil)
		require.NoError(t, err)

		identity, err := svc.Resolve(t.Context(), result.Plaintext)
		require.NoError(t, err)
		require.NotNil(t, identity, "must resolve while still active")

		require.NoError(t, svc.Revoke(t.Context(), tenantID, userID, result.Token.ID))

		identity, err = svc.Resolve(t.Context(), result.Plaintext)
		require.NoError(t, err)
		assert.Nil(t, identity, "a revoked token must not resolve")
	})
}

// TestUserAPITokenService_Resolve is the security-critical path: it must
// return the token owner's CURRENT permissions (live from their Role), not
// anything baked in at token creation -- so a role change or deactivation
// takes effect on existing personal tokens immediately, with nothing to
// keep in sync.
func TestUserAPITokenService_Resolve(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", []string{"alerts", "incidents"})
	svc := service.NewUserAPITokenService(pool, repository.NewUserAPITokenRepository(), repository.NewUserRepository())

	result, err := svc.Create(t.Context(), tenantID, userID, "CI script", nil)
	require.NoError(t, err)

	t.Run("resolves to the user's current tenant/role-derived claims", func(t *testing.T) {
		identity, err := svc.Resolve(t.Context(), result.Plaintext)
		require.NoError(t, err)
		require.NotNil(t, identity)
		assert.Equal(t, tenantID, identity.TenantID)
		assert.Equal(t, userID, identity.UserID)
		assert.False(t, identity.IsAdmin)
		assert.ElementsMatch(t, []string{"alerts", "incidents"}, identity.ResourceAccess)
	})

	t.Run("unknown token string does not resolve", func(t *testing.T) {
		identity, err := svc.Resolve(t.Context(), "pat_does-not-exist")
		require.NoError(t, err)
		assert.Nil(t, identity)
	})

	// Create's public API only ever produces "default 90 days" or "never"
	// (expiresInDays<=0) -- an already-expired token can't be reached
	// through it, so this inserts one directly via the repository (with the
	// same hash algorithm Resolve expects) to exercise that branch.
	t.Run("an already-expired token does not resolve", func(t *testing.T) {
		// token_hash has a global unique constraint (not scoped to tenant)
		// and this test inserts directly via the repository rather than
		// through Create, bypassing whatever collision-avoidance Create's
		// random generation gives every other token in this file -- a
		// fixed literal here collided across repeated runs against a test
		// DB that isn't reset between them (this row is never cleaned up).
		// Folding in tenantID (fresh per test run) keeps the hash unique.
		plaintext := "pat_test-expired-token-" + tenantID.String()
		sum := sha256.Sum256([]byte(plaintext))
		expiredAt := time.Now().Add(-time.Hour)

		require.NoError(t, pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return repository.NewUserAPITokenRepository().Insert(t.Context(), tx, &domain.UserAPIToken{
				TenantID: tenantID, UserID: userID, Name: "expired", TokenHash: hex.EncodeToString(sum[:]), TokenLast4: "oken", ExpiresAt: &expiredAt,
			})
		}))

		identity, err := svc.Resolve(t.Context(), plaintext)
		require.NoError(t, err)
		assert.Nil(t, identity, "an expired token must not resolve")
	})
}
