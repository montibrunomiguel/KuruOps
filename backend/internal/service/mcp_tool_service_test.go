package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// dial/execute (the actual JSON-RPC call to a live MCP server) are not
// exercised here -- same network-dependent boundary already applied to
// LDAP/SAML. These tests cover the policy/lookup logic that runs before any
// network call: server-not-found, approval-state checks, and the
// PendingApprovals/RejectToolCall paths that never dial at all.
func newMCPToolService(t *testing.T) *service.MCPToolService {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	return service.NewMCPToolService(pool, repository.NewMCPServerRepository(), repository.NewAIToolCallRepository(), secrets.NewEnvStore())
}

func TestMCPToolService_DiscoverTools_ServerNotFound(t *testing.T) {
	svc := newMCPToolService(t)
	tenantID := testutil.NewTenant(t)

	_, err := svc.DiscoverTools(t.Context(), tenantID, uuid.New())
	assert.ErrorContains(t, err, "not found")
}

func TestMCPToolService_ProposeToolCall_ServerNotFound(t *testing.T) {
	svc := newMCPToolService(t)
	tenantID := testutil.NewTenant(t)

	_, err := svc.ProposeToolCall(t.Context(), tenantID, uuid.New(), "alert", uuid.New(), "lookup_ip", nil)
	assert.ErrorContains(t, err, "not found")
}

func TestMCPToolService_ProposeToolCall_ToolNotAllowed(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	svc := service.NewMCPToolService(pool, repository.NewMCPServerRepository(), repository.NewAIToolCallRepository(), secrets.NewEnvStore())

	server, err := mcpSvc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"lookup_ip"},
	})
	require.NoError(t, err)

	_, err = svc.ProposeToolCall(t.Context(), tenantID, server.ID, "alert", uuid.New(), "delete_everything", nil)
	assert.ErrorContains(t, err, "not in the allow-list")
}

func TestMCPToolService_ApproveToolCall_NotFound(t *testing.T) {
	svc := newMCPToolService(t)
	tenantID := testutil.NewTenant(t)
	approverID := testutil.NewUser(t, tenantID, "admin", nil)

	err := svc.ApproveToolCall(t.Context(), tenantID, 999999999, approverID)
	assert.ErrorContains(t, err, "not found")
}

func TestMCPToolService_GetToolCall(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	svc := service.NewMCPToolService(pool, repository.NewMCPServerRepository(), repository.NewAIToolCallRepository(), secrets.NewEnvStore())

	server, err := mcpSvc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "EDR", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"quarantine_host"}, SideEffectingTools: []string{"quarantine_host"},
	})
	require.NoError(t, err)
	alertID := uuid.New()
	call, err := svc.ProposeToolCall(t.Context(), tenantID, server.ID, "alert", alertID, "quarantine_host", map[string]any{"host": "10.0.0.5"})
	require.NoError(t, err)

	got, err := svc.GetToolCall(t.Context(), tenantID, call.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "alert", got.ContextType)
	assert.Equal(t, alertID, got.ContextID)

	notFound, err := svc.GetToolCall(t.Context(), tenantID, 999999999)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestMCPToolService_RejectToolCall(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	mcpSvc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	svc := service.NewMCPToolService(pool, repository.NewMCPServerRepository(), repository.NewAIToolCallRepository(), secrets.NewEnvStore())

	server, err := mcpSvc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools:       []string{"quarantine_host"},
		SideEffectingTools: []string{"quarantine_host"},
	})
	require.NoError(t, err)

	// A side-effecting tool call stops at 'proposed' rather than dialing,
	// so this exercises ProposeToolCall's full non-network path.
	call, err := svc.ProposeToolCall(t.Context(), tenantID, server.ID, "incident", uuid.New(), "quarantine_host", map[string]any{"host": "10.0.0.5"})
	require.NoError(t, err)

	pending, err := svc.PendingApprovals(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, call.ID, pending[0].ID)

	require.NoError(t, svc.RejectToolCall(t.Context(), tenantID, call.ID, actorID))

	pending, err = svc.PendingApprovals(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, pending)
}
