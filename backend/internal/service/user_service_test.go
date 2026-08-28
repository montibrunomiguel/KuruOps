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
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewUserService(pool, repository.NewUserRepository(), auditRepo)
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})

	t.Run("rejects a missing email", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, actorID, "", "Someone", "", roleID)
		assert.ErrorContains(t, err, "email is required")
	})

	t.Run("rejects a missing name", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, actorID, "someone@test.local", "  ", "", roleID)
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("creates a local user with a temp password that verifies against the stored hash", func(t *testing.T) {
		user, tempPassword, err := svc.CreateLocal(t.Context(), tenantID, actorID, "newhire@test.local", "New Hire", "", roleID)
		require.NoError(t, err)
		require.NotEmpty(t, tempPassword)
		assert.Equal(t, domain.AuthProviderLocal, user.AuthProvider)
		assert.True(t, user.MustChangePassword)
		assert.Equal(t, roleID, user.RoleID)
		assert.Nil(t, user.Phone)

		got, err := svc.Get(t.Context(), tenantID, user.ID)
		require.NoError(t, err)
		require.NotNil(t, got.PasswordHash)
		ok, err := authn.VerifyPassword(*got.PasswordHash, tempPassword)
		require.NoError(t, err)
		assert.True(t, ok, "the returned temp password must verify against the stored hash")
	})

	t.Run("creates a local user with an optional phone number", func(t *testing.T) {
		user, _, err := svc.CreateLocal(t.Context(), tenantID, actorID, "withphone@test.local", "Has Phone", "+15550100199", roleID)
		require.NoError(t, err)
		require.NotNil(t, user.Phone)
		assert.Equal(t, "+15550100199", *user.Phone)

		got, err := svc.Get(t.Context(), tenantID, user.ID)
		require.NoError(t, err)
		require.NotNil(t, got.Phone)
		assert.Equal(t, "+15550100199", *got.Phone)
	})

	t.Run("rejects a phone without a country code", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, actorID, "badphone@test.local", "Bad Phone", "5511912345678", roleID)
		assert.ErrorContains(t, err, "country code")
	})

	t.Run("rejects a duplicate email within the same tenant", func(t *testing.T) {
		_, _, err := svc.CreateLocal(t.Context(), tenantID, actorID, "newhire@test.local", "Duplicate", "", roleID)
		assert.Error(t, err)
	})

	t.Run("each successful create records an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		require.Len(t, events, 2, "the 2 successful creates above, not the rejected/duplicate ones")
		for _, e := range events {
			assert.Equal(t, "users", e.Area)
			assert.Equal(t, "create", e.Action)
		}
	})
}

func TestUserService_ResetPassword(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})

	t.Run("unknown user", func(t *testing.T) {
		_, err := svc.ResetPassword(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("rejects a federated user", func(t *testing.T) {
		priv, err := authn.GenerateEphemeralKeyPair()
		require.NoError(t, err)
		roleSvc := service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository())
		authSvc := service.NewAuthService(pool, repository.NewTenantRepository(), repository.NewUserRepository(), repository.NewRefreshTokenRepository(), repository.NewMFAPendingTokenRepository(), roleSvc, authn.NewIssuer(priv))
		fedUser, _, _, err := authSvc.ProvisionFederated(t.Context(), tenantID, domain.AuthProviderLDAP, "cn=fed,dc=example,dc=com", "fed@example.com", "Fed User", nil)
		require.NoError(t, err)

		_, err = svc.ResetPassword(t.Context(), tenantID, actorID, fedUser.ID)
		assert.ErrorContains(t, err, "ldap-authenticated user")
	})

	t.Run("resets a local user's password and forces a change on next login", func(t *testing.T) {
		user, originalPassword, err := svc.CreateLocal(t.Context(), tenantID, actorID, "reset-me@test.local", "Reset Me", "", roleID)
		require.NoError(t, err)

		tempPassword, err := svc.ResetPassword(t.Context(), tenantID, actorID, user.ID)
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
	svc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())

	summaries, err := svc.ListSummaries(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, userID, summaries[0].ID)
	assert.NotEmpty(t, summaries[0].Name)
}

func TestUserService_UpdateAccess(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	userID := testutil.NewUser(t, tenantID, "viewer", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewUserService(pool, repository.NewUserRepository(), auditRepo)

	newRoleID := testutil.NewRole(t, tenantID, true, []string{"alerts", "followup"})
	require.NoError(t, svc.UpdateAccess(t.Context(), tenantID, actorID, userID, newRoleID))

	got, err := svc.Get(t.Context(), tenantID, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, newRoleID, got.RoleID)
	assert.True(t, got.Role.IsAdmin)
	assert.Equal(t, domain.ResourceAccess{"alerts", "followup"}, got.Role.ResourceAccess)

	t.Run("records an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 1)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "users", events[0].Area)
		assert.Equal(t, "update-access", events[0].Action)
	})
}

func TestUserService_UpdatePhone(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	userID := testutil.NewUser(t, tenantID, "viewer", nil)
	svc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())

	t.Run("sets a valid phone", func(t *testing.T) {
		require.NoError(t, svc.UpdatePhone(t.Context(), tenantID, actorID, userID, "+5511912345678"))
		got, err := svc.Get(t.Context(), tenantID, userID)
		require.NoError(t, err)
		require.NotNil(t, got.Phone)
		assert.Equal(t, "+5511912345678", *got.Phone)
	})

	t.Run("rejects a phone without a country code", func(t *testing.T) {
		err := svc.UpdatePhone(t.Context(), tenantID, actorID, userID, "5511912345678")
		assert.ErrorContains(t, err, "country code")

		got, err := svc.Get(t.Context(), tenantID, userID)
		require.NoError(t, err)
		require.NotNil(t, got.Phone, "the previously-set phone must survive the rejected update")
		assert.Equal(t, "+5511912345678", *got.Phone)
	})

	t.Run("clears the phone with an empty string", func(t *testing.T) {
		require.NoError(t, svc.UpdatePhone(t.Context(), tenantID, actorID, userID, ""))
		got, err := svc.Get(t.Context(), tenantID, userID)
		require.NoError(t, err)
		assert.Nil(t, got.Phone)
	})
}

func TestUserService_SetActive(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	svc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())

	require.NoError(t, svc.SetActive(t.Context(), tenantID, actorID, userID, false))
	got, err := svc.Get(t.Context(), tenantID, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, got.IsActive)
}

func TestUserService_GroupMappings(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewUserService(pool, repository.NewUserRepository(), auditRepo)
	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts", "followup"})

	t.Run("rejects a provider that isn't ldap or saml", func(t *testing.T) {
		_, err := svc.SaveGroupMapping(t.Context(), tenantID, actorID, domain.AuthProviderLocal, "soc-analysts", roleID)
		assert.ErrorContains(t, err, "only apply to ldap or saml")
	})

	m, err := svc.SaveGroupMapping(t.Context(), tenantID, actorID, domain.AuthProviderLDAP, "soc-analysts", roleID)
	require.NoError(t, err)

	list, err := svc.ListGroupMappings(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, roleID, list[0].RoleID)

	require.NoError(t, svc.DeleteGroupMapping(t.Context(), tenantID, actorID, m.ID))
	list, err = svc.ListGroupMappings(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)

	t.Run("save and delete each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "users", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "save-group-mapping")
		assert.Contains(t, actions, "delete-group-mapping")
	})
}
