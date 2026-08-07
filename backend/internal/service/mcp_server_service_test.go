package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestMCPServerService_Create(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore())

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
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore())

	server, err := svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"lookup_ip"},
	})
	require.NoError(t, err)

	t.Run("updating a nonexistent server fails", func(t *testing.T) {
		_, err := svc.Update(t.Context(), tenantID, uuid.New(), service.MCPServerSaveInput{
			Name: "x", Transport: "http", EndpointOrCommand: "https://x.example.com",
		})
		assert.ErrorContains(t, err, "not found")
	})

	updated, err := svc.Update(t.Context(), tenantID, server.ID, service.MCPServerSaveInput{
		Name: "Threat Intel v2", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
		AllowedTools: []string{"lookup_ip", "lookup_domain"},
	})
	require.NoError(t, err)
	assert.Equal(t, "Threat Intel v2", updated.Name)
	assert.Equal(t, []string{"lookup_ip", "lookup_domain"}, updated.AllowedTools)
}

func TestMCPServerService_SetEnabledAndDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore())

	server, err := svc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
		Name: "Threat Intel", Transport: "http", EndpointOrCommand: "https://mcp.example.com",
	})
	require.NoError(t, err)

	require.NoError(t, svc.SetEnabled(t.Context(), tenantID, server.ID, false))
	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.False(t, list[0].IsEnabled)

	require.NoError(t, svc.Delete(t.Context(), tenantID, server.ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)
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
