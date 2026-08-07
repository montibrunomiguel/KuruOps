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

	t.Run("rejects a missing email", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, "", "Someone", domain.RoleAnalyst, domain.ResourceAccess{"alerts"}, nil)
		assert.ErrorContains(t, err, "email is required")
	})

	t.Run("rejects a missing name", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, "someone@test.local", "  ", domain.RoleAnalyst, domain.ResourceAccess{"alerts"}, nil)
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("rejects an invalid capability", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, "someone@test.local", "Someone", domain.RoleAnalyst, domain.ResourceAccess{"bogus"}, nil)
		assert.ErrorContains(t, err, "invalid resource access capability")
	})

	t.Run("creates a local user with a temp password that verifies against the stored hash", func(t *testing.T) {
		user, tempPassword, err := svc.CreateLocal(t.Context(), tenantID, "newhire@test.local", "New Hire", domain.RoleAnalyst, domain.ResourceAccess{"alerts", "incidents"}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, tempPassword)
		assert.Equal(t, domain.AuthProviderLocal, user.AuthProvider)
		assert.True(t, user.MustChangePassword)
		assert.Equal(t, []string{}, user.AllowedTags, "nil AllowedTags is normalized to empty, not NULL")

		got, err := svc.Get(t.Context(), tenantID, user.ID)
		require.NoError(t, err)
		require.NotNil(t, got.PasswordHash)
		ok, err := authn.VerifyPassword(*got.PasswordHash, tempPassword)
		require.NoError(t, err)
		assert.True(t, ok, "the returned temp password must verify against the stored hash")
	})

	t.Run("rejects a duplicate email within the same tenant", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, "newhire@test.local", "Duplicate", domain.RoleViewer, domain.ResourceAccess{}, nil)
		assert.Error(t, err)
	})
}

func TestUserService_ResetPassword(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewUserService(pool, repository.NewUserRepository())

	t.Run("unknown user", func(t *testing.T) {
		_, err := svc.ResetPassword(t.Context(), tenantID, uuid.New())
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("rejects a federated user", func(t *testing.T) {
		priv, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), authn.NewIssuer(priv))
		fedUser, _, _, err := authSvc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=fed,dc=example,dc=com", "fed@example.com", "Fed User", nil)
		require.NoError(t, err)

		_, err = svc.ResetPassword(t.Context(), tenantID, fedUser.ID)
		assert.ErrorContains(t, err, "ldap-authenticated user")
	})

	t.Run("resets a local user's password and forces a change on next login", func(t *testing.T) {
		user, originalPassword, err := svc.CreateLocal(t.Context(), tenantID, "reset-me@test.local", "Reset Me", domain.RoleAnalyst, domain.ResourceAccess{"alerts"}, nil)
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

	t.Run("rejects an invalid capability before writing", func(t *testing.T) {
		err := svc.UpdateAccess(t.Context(), tenantID, userID, domain.UpdateUserAccessInput{
			Role: domain.RoleAnalyst, ResourceAccess: domain.ResourceAccess{"not-a-real-capability"},
		})
		assert.ErrorContains(t, err, "invalid resource access capability")
	})

	t.Run("valid access is persisted, nil AllowedTags becomes empty", func(t *testing.T) {
		err := svc.UpdateAccess(t.Context(), tenantID, userID, domain.UpdateUserAccessInput{
			Role: domain.RoleAdmin, ResourceAccess: domain.ResourceAccess{"alerts", "followup"},
		})
		require.NoError(t, err)

		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, domain.RoleAdmin, list[0].Role)
		assert.Equal(t, domain.ResourceAccess{"alerts", "followup"}, list[0].ResourceAccess)
		assert.Equal(t, []string{}, list[0].AllowedTags)
	})
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

	t.Run("rejects a provider that isn't ldap or saml", func(t *testing.T) {
		_, err := svc.SaveGroupMapping(t.Context(), tenantID, domain.AuthProviderLocal, "soc-analysts", domain.UpdateUserAccessInput{
			Role: domain.RoleAnalyst, ResourceAccess: domain.ResourceAccess{"alerts"},
		})
		assert.ErrorContains(t, err, "only apply to ldap or saml")
	})

	t.Run("rejects an invalid capability", func(t *testing.T) {
		_, err := svc.SaveGroupMapping(t.Context(), tenantID, domain.AuthProviderLDAP, "soc-analysts", domain.UpdateUserAccessInput{
			Role: domain.RoleAnalyst, ResourceAccess: domain.ResourceAccess{"bogus"},
		})
		assert.ErrorContains(t, err, "invalid resource access capability")
	})

	m, err := svc.SaveGroupMapping(t.Context(), tenantID, domain.AuthProviderLDAP, "soc-analysts", domain.UpdateUserAccessInput{
		Role: domain.RoleAnalyst, ResourceAccess: domain.ResourceAccess{"alerts", "followup"},
	})
	require.NoError(t, err)

	list, err := svc.ListGroupMappings(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, []string{}, list[0].AllowedTags, "nil AllowedTags is normalized to empty, not NULL")

	require.NoError(t, svc.DeleteGroupMapping(t.Context(), tenantID, m.ID))
	list, err = svc.ListGroupMappings(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)
}
