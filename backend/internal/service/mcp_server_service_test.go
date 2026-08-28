package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// fakeMCPServerRepo lets a test fail a specific repo call on demand --
// MCPServerService takes an interface (not the concrete
// *repository.MCPServerRepository) specifically so this is possible. See
// fakeStorageConfigRepo (storage_config_service_test.go) for the fuller
// version of this reasoning.
type fakeMCPServerRepo struct {
	listErr      error
	getErr       error
	insertErr    error
	updateErr    error
	setEnabedErr error
	deleteErr    error
	get          *domain.MCPServer
}

func (f *fakeMCPServerRepo) List(context.Context, pgx.Tx) ([]domain.MCPServer, error) {
	return nil, f.listErr
}
func (f *fakeMCPServerRepo) Get(context.Context, pgx.Tx, uuid.UUID) (*domain.MCPServer, error) {
	return f.get, f.getErr
}
func (f *fakeMCPServerRepo) Insert(context.Context, pgx.Tx, *domain.MCPServer) error {
	return f.insertErr
}
func (f *fakeMCPServerRepo) Update(context.Context, pgx.Tx, *domain.MCPServer) error {
	return f.updateErr
}
func (f *fakeMCPServerRepo) SetEnabled(context.Context, pgx.Tx, uuid.UUID, bool) error {
	return f.setEnabedErr
}
func (f *fakeMCPServerRepo) Delete(context.Context, pgx.Tx, uuid.UUID) error { return f.deleteErr }

func TestMCPServerService_Create(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())

	t.Run("a side-effecting tool must also be allow-listed", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
			Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
			AllowedTools: []string{"lookup_ip"}, SideEffectingTools: []string{"quarantine_host"},
		})
		assert.ErrorContains(t, err, "must also be in allowed_tools")
	})

	server, err := svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AuthToken:          "secret-token",
		AllowedTools:       []string{"lookup_ip", "quarantine_host"},
		SideEffectingTools: []string{"quarantine_host"},
	})
	require.NoError(t, err)
	require.NotNil(t, server.AuthSecretRef)
	assert.NotContains(t, *server.AuthSecretRef, "secret-token", "the plaintext token never lands in the stored ref")

	t.Run("nil EnabledFor normalizes to empty, not NULL", func(t *testing.T) {
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, []string{}, list[0].EnabledFor)
	})
}

func TestMCPServerService_Update(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), auditRepo)

	server, err := svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"lookup_ip"},
	})
	require.NoError(t, err)

	t.Run("updating a nonexistent server fails", func(t *testing.T) {
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), service.MCPServerSaveInput{
			Name: "x", Transport: "http", EndpointOrCommand: "https://x.example.com",
		})
		assert.ErrorContains(t, err, "not found")
	})

	updated, err := svc.Update(t.Context(), tenantID, actorID, server.ID, service.MCPServerSaveInput{
		Name: "Threat Intel v2", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"lookup_ip", "lookup_domain"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Threat Intel v2", updated.Name)
	assert.Equal(t, []string{"lookup_ip", "lookup_domain"}, updated.AllowedTools)

	t.Run("create/update each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "mcp-servers", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "create")
		assert.Contains(t, actions, "update")
	})
}

func TestMCPServerService_SetEnabledAndDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), auditRepo)

	server, err := svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
	})
	require.NoError(t, err)

	require.NoError(t, svc.SetEnabled(t.Context(), tenantID, actorID, server.ID, false))
	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.False(t, list[0].IsEnabled)

	require.NoError(t, svc.Delete(t.Context(), tenantID, actorID, server.ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)

	t.Run("create/set-enabled/delete each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "mcp-servers", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "create")
		assert.Contains(t, actions, "set-enabled")
		assert.Contains(t, actions, "delete")
	})
}

func TestEvaluateToolInvocation(t *testing.T) {
	base := domain.MCPServer{
		IsEnabled:          true,
		AllowedTools:       []string{"lookup_ip", "quarantine_host"},
		SideEffectingTools: []string{"quarantine_host"},
	}

	t.Run("a disabled server allows nothing", func(t *testing.T) {
		disabled := base
		disabled.IsEnabled = false
		policy := service.EvaluateToolInvocation(disabled, "lookup_ip")
		assert.False(t, policy.Allowed)
	})

	t.Run("a tool outside the allow-list is not allowed", func(t *testing.T) {
		policy := service.EvaluateToolInvocation(base, "delete_everything")
		assert.False(t, policy.Allowed)
	})

	t.Run("a plain allowed tool executes without approval", func(t *testing.T) {
		policy := service.EvaluateToolInvocation(base, "lookup_ip")
		assert.True(t, policy.Allowed)
		assert.False(t, policy.RequiresApproval)
	})

	t.Run("a side-effecting tool requires approval even though it's allowed", func(t *testing.T) {
		policy := service.EvaluateToolInvocation(base, "quarantine_host")
		assert.True(t, policy.Allowed)
		assert.True(t, policy.RequiresApproval)
	})
}

// TestMCPServerService_RepoErrors exercises each mutating method's "load
// existing server to build the audit diff, then persist" error-wrapping
// branches -- unreachable via a real Postgres integration test.
func TestMCPServerService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	validInput := service.MCPServerSaveInput{Name: "X", Transport: "http", EndpointOrCommand: "https://example.invalid"}

	t.Run("Create wraps an Insert failure", func(t *testing.T) {
		svc := service.NewMCPServerService(pool, &fakeMCPServerRepo{insertErr: errors.New("insert boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		_, err := svc.Create(t.Context(), tenantID, actorID, validInput)
		assert.ErrorContains(t, err, "insert boom")
	})

	t.Run("Update wraps a Get failure", func(t *testing.T) {
		svc := service.NewMCPServerService(pool, &fakeMCPServerRepo{getErr: errors.New("get boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), validInput)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Update wraps an Update failure", func(t *testing.T) {
		svc := service.NewMCPServerService(pool, &fakeMCPServerRepo{get: &domain.MCPServer{}, updateErr: errors.New("update boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		_, err := svc.Update(t.Context(), tenantID, actorID, uuid.New(), validInput)
		assert.ErrorContains(t, err, "update boom")
	})

	t.Run("SetEnabled wraps a SetEnabled failure", func(t *testing.T) {
		svc := service.NewMCPServerService(pool, &fakeMCPServerRepo{setEnabedErr: errors.New("set-enabled boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		err := svc.SetEnabled(t.Context(), tenantID, actorID, uuid.New(), false)
		assert.ErrorContains(t, err, "set-enabled boom")
	})

	t.Run("Delete wraps a Get failure", func(t *testing.T) {
		svc := service.NewMCPServerService(pool, &fakeMCPServerRepo{getErr: errors.New("get boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Delete wraps a Delete failure", func(t *testing.T) {
		svc := service.NewMCPServerService(pool, &fakeMCPServerRepo{deleteErr: errors.New("delete boom")}, secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
		err := svc.Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "delete boom")
	})
}
