package service_test

import (
	"context"
	"errors"
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
	return service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository())
}

// fakeRoleRepo lets a test fail a specific repo call on demand -- RoleService
// takes an interface (not the concrete *repository.RoleRepository)
// specifically so this is possible. See fakeStorageConfigRepo
// (storage_config_service_test.go) for the fuller version of this
// reasoning.
type fakeRoleRepo struct {
	listErr       error
	getErr        error
	getByNameErr  error
	createErr     error
	updateErr     error
	deleteErr     error
	countUsersErr error
	get           *domain.Role
	countUsers    int
}

func (f *fakeRoleRepo) List(context.Context, pgx.Tx) ([]domain.Role, error) { return nil, f.listErr }
func (f *fakeRoleRepo) Get(context.Context, pgx.Tx, uuid.UUID) (*domain.Role, error) {
	return f.get, f.getErr
}
func (f *fakeRoleRepo) GetByName(context.Context, pgx.Tx, string) (*domain.Role, error) {
	return nil, f.getByNameErr
}
func (f *fakeRoleRepo) Create(context.Context, pgx.Tx, *domain.Role) error { return f.createErr }
func (f *fakeRoleRepo) Update(context.Context, pgx.Tx, *domain.Role) error { return f.updateErr }
func (f *fakeRoleRepo) Delete(context.Context, pgx.Tx, uuid.UUID) error    { return f.deleteErr }
func (f *fakeRoleRepo) CountUsers(context.Context, pgx.Tx, uuid.UUID) (int, error) {
	return f.countUsers, f.countUsersErr
}

func TestRoleService_CreateUpdateList(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	svc := newRoleService(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("rejects a missing name", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, domain.SaveRoleInput{ResourceAccess: domain.ResourceAccess{"alerts"}})
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("rejects an invalid capability", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, domain.SaveRoleInput{Name: "Bad Role", ResourceAccess: domain.ResourceAccess{"bogus"}})
		assert.ErrorContains(t, err, "invalid resource access capability")
	})

	role, err := svc.Create(t.Context(), tenantID, actorID, domain.SaveRoleInput{
		Name: "Analyst", ResourceAccess: domain.ResourceAccess{"alerts", "incidents"}, AllowedTags: nil,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{}, role.AllowedTags, "nil AllowedTags is normalized to empty, not NULL")

	got, err := svc.Get(t.Context(), tenantID, role.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Analyst", got.Name)

	updated, err := svc.Update(t.Context(), tenantID, actorID, role.ID, domain.SaveRoleInput{
		Name: "Senior Analyst", IsAdmin: true, ResourceAccess: domain.ResourceAccess{"alerts", "incidents", "followup"}, AllowedTags: []string{"CompanyA"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Senior Analyst", updated.Name)
	assert.True(t, updated.IsAdmin)

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	names := make([]string, len(list))
	for i, r := range list {
		names[i] = r.Name
	}
	assert.Contains(t, names, "Senior Analyst")

	t.Run("create and update each record an admin audit event", func(t *testing.T) {
		auditRepo := repository.NewAdminAuditEventRepository()
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "roles", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "create")
		assert.Contains(t, actions, "update")
	})
}

func TestRoleService_Delete(t *testing.T) {
	svc := newRoleService(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)

	t.Run("deletes a role with no assigned users", func(t *testing.T) {
		role, err := svc.Create(t.Context(), tenantID, actorID, domain.SaveRoleInput{Name: "Unused", ResourceAccess: domain.ResourceAccess{"alerts"}})
		require.NoError(t, err)

		require.NoError(t, svc.Delete(t.Context(), tenantID, actorID, role.ID))
		got, err := svc.Get(t.Context(), tenantID, role.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("refuses to delete a role still assigned to a user", func(t *testing.T) {
		roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})
		testutil.NewUserWithRole(t, tenantID, roleID)

		err := svc.Delete(t.Context(), tenantID, actorID, roleID)
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

// TestRoleService_RepoErrors exercises each mutating method's "load
// existing role to build the audit diff, then persist" error-wrapping
// branches -- unreachable via a real Postgres integration test.
func TestRoleService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	validInput := domain.SaveRoleInput{Name: "X", ResourceAccess: domain.ResourceAccess{"alerts"}}

	t.Run("Create wraps a Create failure", func(t *testing.T) {
		svc := service.NewRoleService(pool, &fakeRoleRepo{createErr: errors.New("create boom")}, repository.NewAdminAuditEventRepository())
		_, err := svc.Create(t.Context(), tenantID, actorID, validInput)
		assert.ErrorContains(t, err, "create boom")
	})

	t.Run("Update wraps a Get failure", func(t *testing.T) {
		svc := service.NewRoleService(pool, &fakeRoleRepo{getErr: errors.New("get boom")}, repository.NewAdminAuditEventRepository())
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), validInput)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Update wraps an Update failure", func(t *testing.T) {
		svc := service.NewRoleService(pool, &fakeRoleRepo{get: &domain.Role{}, updateErr: errors.New("update boom")}, repository.NewAdminAuditEventRepository())
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), validInput)
		assert.ErrorContains(t, err, "update boom")
	})

	t.Run("Delete wraps a CountUsers failure", func(t *testing.T) {
		svc := service.NewRoleService(pool, &fakeRoleRepo{countUsersErr: errors.New("count boom")}, repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "count boom")
	})

	t.Run("Delete wraps a Get failure", func(t *testing.T) {
		svc := service.NewRoleService(pool, &fakeRoleRepo{getErr: errors.New("get boom")}, repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Delete wraps a Delete failure", func(t *testing.T) {
		svc := service.NewRoleService(pool, &fakeRoleRepo{get: &domain.Role{}, deleteErr: errors.New("delete boom")}, repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "delete boom")
	})
}
