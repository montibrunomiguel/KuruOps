package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestUserService_CreateLocal(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewUserService(pool, repository.NewUserRepository())
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})

	t.Run("rejects a missing email", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, "", "Someone", roleID)
		assert.ErrorContains(t, err, "email is required")
	})

	t.Run("rejects a missing name", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, "someone@test.local", "  ", roleID)
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("creates a local user with a temp password that verifies against the stored hash", func(t *testing.T) {
		user, tempPassword, err := svc.CreateLocal(t.Context(), tenantID, "newhire@test.local", "New Hire", roleID)
		require.NoError(t, err)
		require.NotEmpty(t, tempPassword)
		assert.Equal(t, domain.AuthProviderLocal, user.AuthProvider)
		assert.True(t, user.MustChangePassword)
		assert.Equal(t, roleID, user.RoleID)

		got, err := svc.Get(t.Context(), tenantID, user.ID)
		require.NoError(t, err)
		require.NotNil(t, got.PasswordHash)
		ok, err := authn.VerifyPassword(*got.PasswordHash, tempPassword)
		require.NoError(t, err)
		assert.True(t, ok, "the returned temp password must verify against the stored hash")
	})

	t.Run("rejects a duplicate email within the same tenant", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, "newhire@test.local", "Duplicate", roleID)
		assert.Error(t, err)
	})
}

func TestUserService_ResetPassword(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewUserService(pool, repository.NewUserRepository())
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})

	t.Run("unknown user", func(t *testing.T) {
		_, err := svc.ResetPassword(t.Context(), tenantID, uuid.New())
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("rejects a federated user", func(t *testing.T) {
		priv, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		roleSvc := service.NewRoleService(pool, repository.NewRoleRepository())
		authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), roleSvc, authn.NewIssuer(priv))
		fedUser, _, _, err := authSvc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=fed,dc=example,dc=com", "fed@example.com", "Fed User", nil)
		require.NoError(t, err)

		_, err = svc.ResetPassword(t.Context(), tenantID, fedUser.ID)
		assert.ErrorContains(t, err, "ldap-authenticated user")
	})

	t.Run("resets a local user's password and forces a change on next login", func(t *testing.T) {
		user, originalPassword, err := svc.CreateLocal(t.Context(), tenantID, "reset-me@test.local", "Reset Me", roleID)
		require.NoError(t, err)

		tempPassword, err := svc.ResetPassword(t.Context(), tenantID, user.ID)
		require.NoError(t, err)
		require.NotEmpty(t, tempPassword)
		assert.NotEqual(t, originalPassword, tempPassword)

		got, err := svc.Get(t.Context(), tenantID, user.ID)
		require.NoError(t, err)
		assert.True(t, got.MustChangePassword, "a reset password must force a change on next login")

		okOld, err := authn.VerifyPassword(*got.PasswordHash, originalPassword)
		require.NoError(t, err)
		assert.False(t, okOld, "the old password must stop verifying after a reset")

		okNew, err := authn.VerifyPassword(*got.PasswordHash, tempPassword)
		require.NoError(t, err)
		assert.True(t, okNew, "the returned temp password must verify against the stored hash")
	})
}

func TestUserService_ListSummaries(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewUserService(pool, repository.NewUserRepository())

	summaries, err := svc.ListSummaries(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, userID, summaries[0].ID)
	assert.NotEmpty(t, summaries[0].Name)
}

func TestUserService_UpdateAccess(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "viewer", nil)
	svc := service.NewUserService(pool, repository.NewUserRepository())

	newRoleID := testutil.NewRole(t, tenantID, true, []string{"alerts", "followup"})
	require.NoError(t, svc.UpdateAccess(t.Context(), tenantID, userID, newRoleID))

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, newRoleID, list[0].RoleID)
	assert.True(t, list[0].Role.IsAdmin)
	assert.Equal(t, domain.ResourceAccess{"alerts", "followup"}, list[0].Role.ResourceAccess)
}

func TestUserService_SetActive(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewUserService(pool, repository.NewUserRepository())

	require.NoError(t, svc.SetActive(t.Context(), tenantID, userID, false))
	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.False(t, list[0].IsActive)
}

func TestUserService_GroupMappings(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewUserService(pool, repository.NewUserRepository())
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts", "followup"})

	t.Run("rejects a provider that isn't ldap or saml", func(t *testing.T) {
		_, err := svc.SaveGroupMapping(t.Context(), tenantID, domain.AuthProviderLocal, "soc-analysts", roleID)
		assert.ErrorContains(t, err, "only apply to ldap or saml")
	})

	m, err := svc.SaveGroupMapping(t.Context(), tenantID, domain.AuthProviderLDAP, "soc-analysts", roleID)
	require.NoError(t, err)

	list, err := svc.ListGroupMappings(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, roleID, list[0].RoleID)

	require.NoError(t, svc.DeleteGroupMapping(t.Context(), tenantID, m.ID))
	list, err = svc.ListGroupMappings(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)
}
