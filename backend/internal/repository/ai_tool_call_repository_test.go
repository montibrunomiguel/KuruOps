package repository_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestAIToolCallRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	mcpRepo := repository.NewMCPServerRepository()
	repo := repository.NewAIToolCallRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	server := &domain.MCPServer{
		TenantID:           tenantID,
		Name:               "Test MCP",
		Transport:          "http",
		EndpointOrCommand:  "https://mcp.example.com",
		AllowedTools:       []string{"lookup_ip"},
		EnabledFor:         []string{"alert", "incident"},
		SideEffectingTools: []string{},
	}
	require.NoError(t, mcpRepo.Insert(t.Context(), tx, server))

	contextID := uuid.New()
	call := &domain.AIToolCall{
		TenantID:    tenantID,
		MCPServerID: server.ID,
		ToolName:    "lookup_ip",
		ContextType: "alert",
		ContextID:   contextID,
		Args:        json.RawMessage(`{"ip":"10.0.0.5"}`),
		Status:      domain.ToolCallProposed,
	}
	require.NoError(t, repo.Insert(t.Context(), tx, call))
	require.NotZero(t, call.ID)

	t.Run("get", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, call.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "lookup_ip", got.ToolName)
		assert.Equal(t, domain.ToolCallProposed, got.Status)
	})

	t.Run("get unknown id returns nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, call.ID+999999)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("list pending includes only proposed calls", func(t *testing.T) {
		pending, err := repo.ListPending(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, pending, 1)
		assert.Equal(t, call.ID, pending[0].ID)
	})

	t.Run("set status stamps approver and approved_at", func(t *testing.T) {
		approver := testutil.NewUser(t, tenantID, "analyst", nil)
		require.NoError(t, repo.SetStatus(t.Context(), tx, call.ID, domain.ToolCallApproved, &approver))

		got, err := repo.Get(t.Context(), tx, call.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ToolCallApproved, got.Status)
		require.NotNil(t, got.ApprovedBy)
		assert.Equal(t, approver, *got.ApprovedBy)
		assert.NotNil(t, got.ApprovedAt)
	})

	t.Run("list pending excludes the now-approved call", func(t *testing.T) {
		pending, err := repo.ListPending(t.Context(), tx)
		require.NoError(t, err)
		assert.Empty(t, pending)
	})

	t.Run("set result stores the payload and final status", func(t *testing.T) {
		require.NoError(t, repo.SetResult(t.Context(), tx, call.ID, domain.ToolCallExecuted, json.RawMessage(`{"country":"US"}`)))

		got, err := repo.Get(t.Context(), tx, call.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ToolCallExecuted, got.Status)
		assert.JSONEq(t, `{"country":"US"}`, string(got.Result))
	})
}
