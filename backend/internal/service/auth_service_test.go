package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newAuthService(t *testing.T) (*db.Pool, *service.AuthService) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	priv, err := authn.GenerateEphemeralKeyPair()
	require.NoError(t, err)
	issuer := authn.NewIssuer(priv)
	roleSvc := service.NewRoleService(pool, repository.NewRoleRepository())
	svc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), roleSvc, issuer)
	return pool, svc
}

func TestAuthService_ResolveDefaultTenant(t *testing.T) {
	_, svc := newAuthService(t)
	tenant, err := svc.ResolveDefaultTenant(t.Context())
	require.NoError(t, err)
	require.NotNil(t, tenant, "the seed migration guarantees at least one tenant exists")
}

func TestAuthService_LoginLocal(t *testing.T) {
	_, svc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", []string{"alerts"})

	t.Run("correct credentials return a user, a token, and a refresh token", func(t *testing.T) {
		user, token, refreshToken, err := svc.LoginLocal(t.Context(), tenantID, emailFor(t, tenantID, userID), testutil.TestPassword)
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.NotEmpty(t, token)
		assert.NotEmpty(t, refreshToken)
		assert.Equal(t, userID, user.ID)
	})

	t.Run("wrong password returns no error and no user -- indistinguishable from unknown email", func(t *testing.T) {
		user, token, refreshToken, err := svc.LoginLocal(t.Context(), tenantID, emailFor(t, tenantID, userID), "wrong-password")
		require.NoError(t, err)
		assert.Nil(t, user)
		assert.Empty(t, token)
		assert.Empty(t, refreshToken)
	})

	t.Run("unknown email returns no error and no user", func(t *testing.T) {
		user, token, refreshToken, err := svc.LoginLocal(t.Context(), tenantID, "nobody@test.local", testutil.TestPassword)
		require.NoError(t, err)
		assert.Nil(t, user)
		assert.Empty(t, token)
		assert.Empty(t, refreshToken)
	})

	t.Run("an inactive user cannot log in", func(t *testing.T) {
		inactiveID := testutil.NewUser(t, tenantID, "analyst", nil)
		userSvc := service.NewUserService(testutil.RequireTestDB(t), repository.NewUserRepository())
		require.NoError(t, userSvc.SetActive(t.Context(), tenantID, inactiveID, false))

		user, _, _, err := svc.LoginLocal(t.Context(), tenantID, emailFor(t, tenantID, inactiveID), testutil.TestPassword)
		require.NoError(t, err)
		assert.Nil(t, user)
	})
}

func TestAuthService_Refresh(t *testing.T) {
	_, svc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", []string{"alerts"})

	_, _, refreshToken, err := svc.LoginLocal(t.Context(), tenantID, emailFor(t, tenantID, userID), testutil.TestPassword)
	require.NoError(t, err)
	require.NotEmpty(t, refreshToken)

	t.Run("a valid refresh token yields a new access token and rotates the refresh token", func(t *testing.T) {
		newToken, newRefreshToken, err := svc.Refresh(t.Context(), tenantID, refreshToken)
		require.NoError(t, err)
		assert.NotEmpty(t, newToken)
		assert.NotEmpty(t, newRefreshToken)
		assert.NotEqual(t, refreshToken, newRefreshToken)

		t.Run("the rotated-away old token can no longer be used", func(t *testing.T) {
			token, rt, err := svc.Refresh(t.Context(), tenantID, refreshToken)
			require.NoError(t, err)
			assert.Empty(t, token)
			assert.Empty(t, rt)
		})

		refreshToken = newRefreshToken
	})

	t.Run("an unknown refresh token yields no error and no token", func(t *testing.T) {
		token, rt, err := svc.Refresh(t.Context(), tenantID, "rt_no-such-token")
		require.NoError(t, err)
		assert.Empty(t, token)
		assert.Empty(t, rt)
	})

	t.Run("RevokeSessions invalidates the current refresh token", func(t *testing.T) {
		require.NoError(t, svc.RevokeSessions(t.Context(), tenantID, userID))

		token, rt, err := svc.Refresh(t.Context(), tenantID, refreshToken)
		require.NoError(t, err)
		assert.Empty(t, token)
		assert.Empty(t, rt)
	})
}

func TestAuthService_ChangePassword(t *testing.T) {
	_, svc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("rejects a new password shorter than 8 characters", func(t *testing.T) {
		_, err := svc.ChangePassword(t.Context(), tenantID, userID, testutil.TestPassword, "short")
		assert.ErrorContains(t, err, "at least 8 characters")
	})

	t.Run("rejects the wrong current password", func(t *testing.T) {
		_, err := svc.ChangePassword(t.Context(), tenantID, userID, "wrong-current-password", "NewPassword123!")
		assert.ErrorContains(t, err, "incorrect")
	})

	t.Run("a correct change returns a fresh token and clears must_change_password", func(t *testing.T) {
		token, err := svc.ChangePassword(t.Context(), tenantID, userID, testutil.TestPassword, "NewPassword123!")
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		user, loginToken, _, err := svc.LoginLocal(t.Context(), tenantID, emailFor(t, tenantID, userID), "NewPassword123!")
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.NotEmpty(t, loginToken)
	})
}

func TestAuthService_UpdateProfile(t *testing.T) {
	pool, svc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("rejects a missing name", func(t *testing.T) {
		_, err := svc.UpdateProfile(t.Context(), tenantID, userID, "  ", "someone@test.local", "")
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("name-only change succeeds without a password", func(t *testing.T) {
		email := emailFor(t, tenantID, userID)
		user, err := svc.UpdateProfile(t.Context(), tenantID, userID, "New Display Name", email, "")
		require.NoError(t, err)
		assert.Equal(t, "New Display Name", user.Name)
		assert.Equal(t, email, user.Email)
	})

	t.Run("email change without the correct current password is rejected", func(t *testing.T) {
		_, err := svc.UpdateProfile(t.Context(), tenantID, userID, "New Display Name", "changed@test.local", "wrong-password")
		assert.ErrorContains(t, err, "incorrect")

		// the email must not have changed
		email := emailFor(t, tenantID, userID)
		assert.NotEqual(t, "changed@test.local", email)
	})

	t.Run("email change with the correct current password succeeds", func(t *testing.T) {
		user, err := svc.UpdateProfile(t.Context(), tenantID, userID, "New Display Name", "changed@test.local", testutil.TestPassword)
		require.NoError(t, err)
		assert.Equal(t, "changed@test.local", user.Email)
	})

	t.Run("rejects a federated user", func(t *testing.T) {
		priv, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		fedSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), service.NewRoleService(pool, repository.NewRoleRepository()), authn.NewIssuer(priv))
		fedUser, _, _, err := fedSvc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=fed2,dc=example,dc=com", "fed2@example.com", "Fed User", nil)
		require.NoError(t, err)

		_, err = svc.UpdateProfile(t.Context(), tenantID, fedUser.ID, "New Name", "fed2@example.com", "")
		assert.ErrorContains(t, err, "local accounts")
	})
}

func TestAuthService_ProvisionFederated(t *testing.T) {
	pool, svc := newAuthService(t)
	tenantID := testutil.NewTenant(t)
	userSvc := service.NewUserService(pool, repository.NewUserRepository())

	t.Run("unmapped groups fall back to the least-privilege default", func(t *testing.T) {
		user, token, refreshToken, err := svc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=jdoe,dc=example,dc=com", "jdoe@example.com", "Jane Doe", []string{"no-such-group"})
		require.NoError(t, err)
		assert.NotEmpty(t, token)
		assert.NotEmpty(t, refreshToken)
		require.NotNil(t, user.Role)
		assert.False(t, user.Role.IsAdmin)
		assert.Equal(t, domain.ResourceAccess{domain.ResourceCapabilityAlerts, domain.ResourceCapabilityIncidents}, user.Role.ResourceAccess)
		assert.NotEmpty(t, user.Role.AllowedTags, "an unmapped user gets a sentinel tag, not unrestricted access")
	})

	t.Run("a matching group mapping wins over the default", func(t *testing.T) {
		roleID := testutil.NewRole(t, tenantID, false, []string{"alerts", "followup"})
		_, err := userSvc.SaveGroupMapping(t.Context(), tenantID, domain.AuthProviderLDAP, "soc-analysts", roleID)
		require.NoError(t, err)

		user, _, _, err := svc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=asmith,dc=example,dc=com", "asmith@example.com", "Alex Smith", []string{"soc-analysts"})
		require.NoError(t, err)
		require.NotNil(t, user.Role)
		assert.Equal(t, roleID, user.RoleID)
		assert.Equal(t, domain.ResourceAccess{"alerts", "followup"}, user.Role.ResourceAccess)
	})

	t.Run("re-provisioning the same external identity updates rather than duplicates", func(t *testing.T) {
		user1, _, _, err := svc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=repeat,dc=example,dc=com", "repeat@example.com", "First Name", nil)
		require.NoError(t, err)

		user2, _, _, err := svc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=repeat,dc=example,dc=com", "repeat@example.com", "Second Name", nil)
		require.NoError(t, err)
		assert.Equal(t, user1.ID, user2.ID)
		assert.Equal(t, "Second Name", user2.Name)
	})
}

// emailFor loads the fixture user's generated email back out of the
// database -- testutil.NewUser doesn't return it directly since most
// repository tests only need the id.
func emailFor(t *testing.T, tenantID, userID uuid.UUID) string {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tx := testutil.BeginTx(t, pool, tenantID)
	u, err := repository.NewUserRepository().Get(t.Context(), tx, userID)
	require.NoError(t, err)
	require.NotNil(t, u)
	return u.Email
}
