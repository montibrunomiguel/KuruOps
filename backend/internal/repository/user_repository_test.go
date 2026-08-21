package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestUserRepository_GetAndList(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", []string{"alerts"})
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	got, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, userID, got.ID)
	require.NotNil(t, got.Role)
	assert.False(t, got.Role.IsAdmin)
	assert.Equal(t, domain.ResourceAccess{"alerts"}, got.Role.ResourceAccess)
	require.NotNil(t, got.PasswordHash)

	list, err := repo.List(t.Context(), tx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, userID, list[0].ID)

	t.Run("get unknown id returns nil, nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, tenantID, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("get with the wrong tenant id returns nil, nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, testutil.NewTenant(t), userID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestUserRepository_ListSummaries(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	activeID := testutil.NewUser(t, tenantID, "analyst", nil)
	inactiveID := testutil.NewUser(t, tenantID, "viewer", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.SetActive(t.Context(), tx, inactiveID, false))

	summaries, err := repo.ListSummaries(t.Context(), tx)
	require.NoError(t, err)

	ids := make([]uuid.UUID, len(summaries))
	for i, s := range summaries {
		ids[i] = s.ID
		assert.NotEmpty(t, s.Name)
	}
	assert.Contains(t, ids, activeID)
	assert.NotContains(t, ids, inactiveID, "a deactivated user isn't a valid owner/assignee pick")
}

func TestUserRepository_GetByEmail(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "viewer", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	created, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)

	t.Run("existing email", func(t *testing.T) {
		got, err := repo.GetByEmail(t.Context(), tx, tenantID, created.Email)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, userID, got.ID)
	})

	t.Run("unknown email returns nil, not an error", func(t *testing.T) {
		got, err := repo.GetByEmail(t.Context(), tx, tenantID, "does-not-exist@test.local")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestUserRepository_UpsertFederated(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts", "incidents"})
	externalID := "cn=jdoe,dc=example,dc=com"
	u := &domain.User{
		TenantID:     tenantID,
		Email:        "federated@test.local",
		Name:         "Federated User",
		AuthProvider: domain.AuthProviderLDAP,
		ExternalID:   &externalID,
		RoleID:       roleID,
	}
	require.NoError(t, repo.UpsertFederated(t.Context(), tx, u))
	require.NotEqual(t, [16]byte{}, u.ID)
	assert.True(t, u.IsActive)

	t.Run("second call with the same tenant+email updates instead of duplicating", func(t *testing.T) {
		firstID := u.ID
		otherRoleID := testutil.NewRole(t, tenantID, false, []string{"followup"})
		u2 := &domain.User{
			TenantID:     tenantID,
			Email:        "federated@test.local",
			Name:         "Federated User Renamed",
			AuthProvider: domain.AuthProviderLDAP,
			ExternalID:   &externalID,
			RoleID:       otherRoleID,
		}
		require.NoError(t, repo.UpsertFederated(t.Context(), tx, u2))
		assert.Equal(t, firstID, u2.ID)

		got, err := repo.Get(t.Context(), tx, tenantID, firstID)
		require.NoError(t, err)
		assert.Equal(t, "Federated User Renamed", got.Name)
		assert.Equal(t, otherRoleID, got.RoleID)
	})
}

func TestUserRepository_CreateLocal(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})
	phone := "+1 555-0100"
	u := &domain.User{
		TenantID: tenantID,
		Email:    "newlocal@test.local",
		Name:     "New Local User",
		RoleID:   roleID,
		Phone:    &phone,
	}
	require.NoError(t, repo.CreateLocal(t.Context(), tx, u, "$argon2id$fake-hash"))
	require.NotEqual(t, [16]byte{}, u.ID)
	assert.True(t, u.IsActive)
	assert.True(t, u.MustChangePassword, "admin-created local users must change their temp password on first login")

	got, err := repo.Get(t.Context(), tx, tenantID, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AuthProviderLocal, got.AuthProvider)
	assert.Equal(t, "New Local User", got.Name)
	assert.Equal(t, roleID, got.RoleID)
	require.NotNil(t, got.PasswordHash)
	assert.Equal(t, "$argon2id$fake-hash", *got.PasswordHash)
	require.NotNil(t, got.Phone)
	assert.Equal(t, phone, *got.Phone)

	t.Run("duplicate email within the same tenant fails", func(t *testing.T) {
		dupe := &domain.User{
			TenantID: tenantID,
			Email:    "newlocal@test.local",
			Name:     "Someone Else",
			RoleID:   roleID,
		}
		err := repo.CreateLocal(t.Context(), tx, dupe, "$argon2id$fake-hash-2")
		require.Error(t, err)
	})
}

func TestUserRepository_UpdateAccess(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "viewer", []string{})
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	newRoleID := testutil.NewRole(t, tenantID, true, []string{"alerts", "incidents", "followup"})
	err := repo.UpdateAccess(t.Context(), tx, userID, newRoleID)
	require.NoError(t, err)

	got, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	assert.Equal(t, newRoleID, got.RoleID)
	assert.True(t, got.Role.IsAdmin)
	assert.Equal(t, domain.ResourceAccess{"alerts", "incidents", "followup"}, got.Role.ResourceAccess)
}

func TestUserRepository_UpdateProfile(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	phone := "+1 555-0100"
	require.NoError(t, repo.UpdateProfile(t.Context(), tx, userID, "Renamed User", "renamed@test.local", &phone))

	got, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	assert.Equal(t, "Renamed User", got.Name)
	assert.Equal(t, "renamed@test.local", got.Email)
	require.NotNil(t, got.Phone)
	assert.Equal(t, phone, *got.Phone)
}

// TestUserRepository_UpdatePhone confirms the narrow phone-only update
// doesn't touch name/email/role -- unlike UpdateProfile, which requires
// resending all three.
func TestUserRepository_UpdatePhone(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	before, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)

	phone := "+5511912345678"
	require.NoError(t, repo.UpdatePhone(t.Context(), tx, userID, &phone))

	got, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	require.NotNil(t, got.Phone)
	assert.Equal(t, phone, *got.Phone)
	assert.Equal(t, before.Name, got.Name)
	assert.Equal(t, before.Email, got.Email)

	require.NoError(t, repo.UpdatePhone(t.Context(), tx, userID, nil))
	cleared, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	assert.Nil(t, cleared.Phone)
}

func TestUserRepository_SetPassword(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.SetPassword(t.Context(), tx, userID, "$argon2id$newhash"))

	got, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	require.NotNil(t, got.PasswordHash)
	assert.Equal(t, "$argon2id$newhash", *got.PasswordHash)
	assert.False(t, got.MustChangePassword, "SetPassword must always clear must_change_password")
}

func TestUserRepository_SetPasswordAndForceChange(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.SetPasswordAndForceChange(t.Context(), tx, userID, "$argon2id$temphash"))

	got, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	require.NotNil(t, got.PasswordHash)
	assert.Equal(t, "$argon2id$temphash", *got.PasswordHash)
	assert.True(t, got.MustChangePassword, "SetPasswordAndForceChange must always set must_change_password -- this is the admin-reset path, the temp password must not become a permanent one")
}

func TestUserRepository_SetActive(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.SetActive(t.Context(), tx, userID, false))
	got, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	assert.False(t, got.IsActive)

	require.NoError(t, repo.SetActive(t.Context(), tx, userID, true))
	got, err = repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	assert.True(t, got.IsActive)
}

func TestUserRepository_StampLastLogin(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	before, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	assert.Nil(t, before.LastLoginAt)

	require.NoError(t, repo.StampLastLogin(t.Context(), tx, userID))

	after, err := repo.Get(t.Context(), tx, tenantID, userID)
	require.NoError(t, err)
	assert.NotNil(t, after.LastLoginAt)
}

func TestUserRepository_GroupMappings(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts", "followup"})
	m := &domain.AuthGroupMapping{
		TenantID:      tenantID,
		Provider:      domain.AuthProviderSAML,
		ExternalGroup: "soc-analysts",
		RoleID:        roleID,
	}
	require.NoError(t, repo.UpsertGroupMapping(t.Context(), tx, m))
	require.NotEqual(t, [16]byte{}, m.ID)

	list, err := repo.ListGroupMappings(t.Context(), tx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "soc-analysts", list[0].ExternalGroup)

	t.Run("upsert on conflict updates in place", func(t *testing.T) {
		adminRoleID := testutil.NewRole(t, tenantID, true, []string{"alerts", "incidents", "followup"})
		m2 := &domain.AuthGroupMapping{
			TenantID:      tenantID,
			Provider:      domain.AuthProviderSAML,
			ExternalGroup: "soc-analysts",
			RoleID:        adminRoleID,
		}
		require.NoError(t, repo.UpsertGroupMapping(t.Context(), tx, m2))
		assert.Equal(t, m.ID, m2.ID)

		list, err := repo.ListGroupMappings(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, adminRoleID, list[0].RoleID)
		assert.True(t, list[0].Role.IsAdmin)
	})

	t.Run("delete removes the mapping", func(t *testing.T) {
		require.NoError(t, repo.DeleteGroupMapping(t.Context(), tx, m.ID))
		list, err := repo.ListGroupMappings(t.Context(), tx)
		require.NoError(t, err)
		assert.Empty(t, list)
	})
}
