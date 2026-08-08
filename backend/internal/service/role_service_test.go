package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newRoleService(t *testing.T) *service.RoleService {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	return service.NewRoleService(pool, repository.NewRoleRepository())
}

func TestRoleService_CreateUpdateList(t *testing.T) {
	svc := newRoleService(t)
	tenantID := testutil.NewTenant(t)

	t.Run("rejects a missing name", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, domain.SaveRoleInput{ResourceAccess: domain.ResourceAccess{"alerts"}})
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("rejects an invalid capability", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, domain.SaveRoleInput{Name: "Bad Role", ResourceAccess: domain.ResourceAccess{"bogus"}})
		assert.ErrorContains(t, err, "invalid resource access capability")
	})

	role, err := svc.Create(t.Context(), tenantID, domain.SaveRoleInput{
		Name: "Analyst", ResourceAccess: domain.ResourceAccess{"alerts", "incidents"}, AllowedTags: nil,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{}, role.AllowedTags, "nil AllowedTags is normalized to empty, not NULL")

	got, err := svc.Get(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Analyst", got.Name)

	updated, err := svc.Update(t.Context(), tenantID, role.ID, domain.SaveRoleInput{
		Name: "Senior Analyst", IsAdmin: true, ResourceAccess: domain.ResourceAccess{"alerts", "incidents", "followup"}, AllowedTags: []string{"CompanyA"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Senior Analyst", updated.Name)
	assert.True(t, updated.IsAdmin)

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Senior Analyst", list[0].Name)
}

func TestRoleService_Delete(t *testing.T) {
	svc := newRoleService(t)
	tenantID := testutil.NewTenant(t)

	t.Run("deletes a role with no assigned users", func(t *testing.T) {
		role, err := svc.Create(t.Context(), tenantID, domain.SaveRoleInput{Name: "Unused", ResourceAccess: domain.ResourceAccess{"alerts"}})
		require.NoError(t, err)

		require.NoError(t, svc.Delete(t.Context(), tenantID, role.ID))
		got, err := svc.Get(t.Context(), tenantID, role.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("refuses to delete a role still assigned to a user", func(t *testing.T) {
		roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})
		testutil.NewUserWithRole(t, tenantID, roleID)

		err := svc.Delete(t.Context(), tenantID, roleID)
		assert.ErrorContains(t, err, "assigned to")

		got, err := svc.Get(t.Context(), tenantID, roleID)
		require.NoError(t, err)
		assert.NotNil(t, got, "the role must still exist after a refused delete")
	})
}

func TestRoleService_EnsureUnmappedFallback(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	svc := newRoleService(t)
	tenantID := testutil.NewTenant(t)

	var firstID, secondID uuid.UUID
	err := pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		id, err := svc.EnsureUnmappedFallback(t.Context(), tx, tenantID)
		firstID = id
		return err
	})
	require.NoError(t, err)

	err = pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
		id, err := svc.EnsureUnmappedFallback(t.Context(), tx, tenantID)
		secondID = id
		return err
	})
	require.NoError(t, err)

	assert.Equal(t, firstID, secondID, "a second call must reuse the same fallback role, not create a duplicate")

	role, err := svc.Get(t.Context(), tenantID, firstID)
	require.NoError(t, err)
	require.NotNil(t, role)
	assert.False(t, role.IsAdmin)
	assert.NotEmpty(t, role.AllowedTags, "the fallback role's tag scope must be a restrictive sentinel, not unrestricted")
}
