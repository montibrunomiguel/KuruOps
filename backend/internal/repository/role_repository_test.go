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

func TestRoleRepository_CreateGetList(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewRoleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	role := &domain.Role{
		TenantID: tenantID, Name: "SOC L1", IsAdmin: false,
		ResourceAccess: domain.ResourceAccess{"alerts", "incidents"}, AllowedTags: []string{"CompanyA"},
	}
	require.NoError(t, repo.Create(t.Context(), tx, role))
	require.NotEqual(t, [16]byte{}, role.ID)

	got, err := repo.Get(t.Context(), tx, role.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "SOC L1", got.Name)
	assert.False(t, got.IsAdmin)
	assert.Equal(t, domain.ResourceAccess{"alerts", "incidents"}, got.ResourceAccess)
	assert.Equal(t, []string{"CompanyA"}, got.AllowedTags)

	byName, err := repo.GetByName(t.Context(), tx, "SOC L1")
	require.NoError(t, err)
	require.NotNil(t, byName)
	assert.Equal(t, role.ID, byName.ID)

	list, err := repo.List(t.Context(), tx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, role.ID, list[0].ID)

	t.Run("get unknown id returns nil, nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("GetByName unknown name returns nil, nil", func(t *testing.T) {
		got, err := repo.GetByName(t.Context(), tx, "no such role")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestRoleRepository_Update(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewRoleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	role := &domain.Role{TenantID: tenantID, Name: "Analyst", ResourceAccess: domain.ResourceAccess{"alerts"}, AllowedTags: []string{}}
	require.NoError(t, repo.Create(t.Context(), tx, role))

	role.Name = "Senior Analyst"
	role.IsAdmin = true
	role.ResourceAccess = domain.ResourceAccess{"alerts", "incidents", "followup"}
	role.AllowedTags = []string{"CompanyB"}
	require.NoError(t, repo.Update(t.Context(), tx, role))

	got, err := repo.Get(t.Context(), tx, role.ID)
	require.NoError(t, err)
	assert.Equal(t, "Senior Analyst", got.Name)
	assert.True(t, got.IsAdmin)
	assert.Equal(t, domain.ResourceAccess{"alerts", "incidents", "followup"}, got.ResourceAccess)
	assert.Equal(t, []string{"CompanyB"}, got.AllowedTags)
}

func TestRoleRepository_DeleteAndCountUsers(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewRoleRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	role := &domain.Role{TenantID: tenantID, Name: "Unused Role", ResourceAccess: domain.ResourceAccess{}, AllowedTags: []string{}}
	require.NoError(t, repo.Create(t.Context(), tx, role))

	count, err := repo.CountUsers(t.Context(), tx, role.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	require.NoError(t, repo.Delete(t.Context(), tx, role.ID))
	got, err := repo.Get(t.Context(), tx, role.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestRoleRepository_CountUsers_ReflectsAssignedUsers(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewRoleRepository()

	roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})
	testutil.NewUser(t, tenantID, "analyst", nil) // uses its own separate role, must not be counted
	tx := testutil.BeginTx(t, pool, tenantID)

	count, err := repo.CountUsers(t.Context(), tx, roleID)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "no user was assigned this specific role yet")
}
