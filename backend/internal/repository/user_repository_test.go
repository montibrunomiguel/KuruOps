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

	got, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, userID, got.ID)
	assert.Equal(t, domain.UserRole("analyst"), got.Role)
	assert.Equal(t, domain.ResourceAccess{"alerts"}, got.ResourceAccess)
	require.NotNil(t, got.PasswordHash)

	list, err := repo.List(t.Context(), tx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, userID, list[0].ID)
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

	created, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)

	t.Run("existing email", func(t *testing.T) {
		got, err := repo.GetByEmail(t.Context(), tx, created.Email)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, userID, got.ID)
	})

	t.Run("unknown email returns nil, not an error", func(t *testing.T) {
		got, err := repo.GetByEmail(t.Context(), tx, "does-not-exist@test.local")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestUserRepository_UpsertFederated(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	externalID := "cn=jdoe,dc=example,dc=com"
	u := &domain.User{
		TenantID:       tenantID,
		Email:          "federated@test.local",
		Name:           "Federated User",
		AuthProvider:   domain.AuthProviderLDAP,
		ExternalID:     &externalID,
		Role:           domain.RoleAnalyst,
		ResourceAccess: domain.ResourceAccess{"alerts", "incidents"},
		AllowedTags:    []string{},
	}
	require.NoError(t, repo.UpsertFederated(t.Context(), tx, u))
	require.NotEqual(t, [16]byte{}, u.ID)
	assert.True(t, u.IsActive)

	t.Run("second call with the same tenant+email updates instead of duplicating", func(t *testing.T) {
		firstID := u.ID
		u2 := &domain.User{
			TenantID:       tenantID,
			Email:          "federated@test.local",
			Name:           "Federated User Renamed",
			AuthProvider:   domain.AuthProviderLDAP,
			ExternalID:     &externalID,
			Role:           domain.RoleViewer,
			ResourceAccess: domain.ResourceAccess{"followup"},
			AllowedTags:    []string{},
		}
		require.NoError(t, repo.UpsertFederated(t.Context(), tx, u2))
		assert.Equal(t, firstID, u2.ID)

		got, err := repo.Get(t.Context(), tx, firstID)
		require.NoError(t, err)
		assert.Equal(t, "Federated User Renamed", got.Name)
		assert.Equal(t, domain.RoleViewer, got.Role)
	})
}

func TestUserRepository_CreateLocal(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	u := &domain.User{
		TenantID:       tenantID,
		Email:          "newlocal@test.local",
		Name:           "New Local User",
		Role:           domain.RoleAnalyst,
		ResourceAccess: domain.ResourceAccess{"alerts"},
		AllowedTags:    []string{},
	}
	require.NoError(t, repo.CreateLocal(t.Context(), tx, u, "$argon2id$fake-hash"))
	require.NotEqual(t, [16]byte{}, u.ID)
	assert.True(t, u.IsActive)
	assert.True(t, u.MustChangePassword, "admin-created local users must change their temp password on first login")

	got, err := repo.Get(t.Context(), tx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AuthProviderLocal, got.AuthProvider)
	assert.Equal(t, "New Local User", got.Name)
	assert.Equal(t, domain.RoleAnalyst, got.Role)
	require.NotNil(t, got.PasswordHash)
	assert.Equal(t, "$argon2id$fake-hash", *got.PasswordHash)

	t.Run("duplicate email within the same tenant fails", func(t *testing.T) {
		dupe := &domain.User{
			TenantID:       tenantID,
			Email:          "newlocal@test.local",
			Name:           "Someone Else",
			Role:           domain.RoleViewer,
			ResourceAccess: domain.ResourceAccess{},
			AllowedTags:    []string{},
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

	err := repo.UpdateAccess(t.Context(), tx, userID, domain.RoleAdmin, domain.ResourceAccess{"alerts", "incidents", "followup"}, []string{"CompanyA"})
	require.NoError(t, err)

	got, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	assert.Equal(t, domain.RoleAdmin, got.Role)
	assert.Equal(t, domain.ResourceAccess{"alerts", "incidents", "followup"}, got.ResourceAccess)
	assert.Equal(t, []string{"CompanyA"}, got.AllowedTags)
}

func TestUserRepository_UpdateProfile(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.UpdateProfile(t.Context(), tx, userID, "Renamed User", "renamed@test.local"))

	got, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	assert.Equal(t, "Renamed User", got.Name)
	assert.Equal(t, "renamed@test.local", got.Email)
}

func TestUserRepository_SetPassword(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.SetPassword(t.Context(), tx, userID, "$argon2id$newhash"))

	got, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	require.NotNil(t, got.PasswordHash)
	assert.Equal(t, "$argon2id$newhash", *got.PasswordHash)
	assert.False(t, got.MustChangePassword, "SetPassword must always clear must_change_password")
}

func TestUserRepository_SetActive(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	require.NoError(t, repo.SetActive(t.Context(), tx, userID, false))
	got, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	assert.False(t, got.IsActive)

	require.NoError(t, repo.SetActive(t.Context(), tx, userID, true))
	got, err = repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	assert.True(t, got.IsActive)
}

func TestUserRepository_StampLastLogin(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	userID := testutil.NewUser(t, tenantID, "analyst", nil)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	before, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	assert.Nil(t, before.LastLoginAt)

	require.NoError(t, repo.StampLastLogin(t.Context(), tx, userID))

	after, err := repo.Get(t.Context(), tx, userID)
	require.NoError(t, err)
	assert.NotNil(t, after.LastLoginAt)
}

func TestUserRepository_GroupMappings(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewUserRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	m := &domain.AuthGroupMapping{
		TenantID:       tenantID,
		Provider:       domain.AuthProviderSAML,
		ExternalGroup:  "soc-analysts",
		Role:           domain.RoleAnalyst,
		ResourceAccess: domain.ResourceAccess{"alerts", "followup"},
		AllowedTags:    []string{},
	}
	require.NoError(t, repo.UpsertGroupMapping(t.Context(), tx, m))
	require.NotEqual(t, [16]byte{}, m.ID)

	list, err := repo.ListGroupMappings(t.Context(), tx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "soc-analysts", list[0].ExternalGroup)

	t.Run("upsert on conflict updates in place", func(t *testing.T) {
		m2 := &domain.AuthGroupMapping{
			TenantID:       tenantID,
			Provider:       domain.AuthProviderSAML,
			ExternalGroup:  "soc-analysts",
			Role:           domain.RoleAdmin,
			ResourceAccess: domain.ResourceAccess{"alerts", "incidents", "followup"},
			AllowedTags:    []string{},
		}
		require.NoError(t, repo.UpsertGroupMapping(t.Context(), tx, m2))
		assert.Equal(t, m.ID, m2.ID)

		list, err := repo.ListGroupMappings(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, domain.RoleAdmin, list[0].Role)
	})

	t.Run("delete removes the mapping", func(t *testing.T) {
		require.NoError(t, repo.DeleteGroupMapping(t.Context(), tx, m.ID))
		list, err := repo.ListGroupMappings(t.Context(), tx)
		require.NoError(t, err)
		assert.Empty(t, list)
	})
}
