package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestMCPServerRepository_InsertGetListUpdateDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewMCPServerRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	s := &domain.MCPServer{
		TenantID:           tenantID,
		Name:               "Threat Intel MCP",
		Transport:          "http",
		EndpointOrCommand:  "https://mcp.example.com",
		AllowedTools:       []string{"lookup_ip", "quarantine_host"},
		EnabledFor:         []string{"alert", "incident"},
		SideEffectingTools: []string{"quarantine_host"},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, s))
	require.NotEqual(t, [16]byte{}, s.ID)
	assert.True(t, s.IsEnabled, "servers are enabled by default on insert")

	t.Run("get", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Threat Intel MCP", got.Name)
		assert.Equal(t, []string{"lookup_ip", "quarantine_host"}, got.AllowedTools)
		assert.Equal(t, []string{"quarantine_host"}, got.SideEffectingTools)
	})

	t.Run("get unknown id returns nil", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("list", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 1)
	})

	t.Run("update", func(t *testing.T) {
		s.Name = "Threat Intel MCP v2"
		s.AllowedTools = []string{"lookup_ip"}
		require.NoError(t, repo.Update(t.Context(), tx, s))

		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		assert.Equal(t, "Threat Intel MCP v2", got.Name)
		assert.Equal(t, []string{"lookup_ip"}, got.AllowedTools)
	})

	t.Run("set enabled", func(t *testing.T) {
		require.NoError(t, repo.SetEnabled(t.Context(), tx, s.ID, false))
		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		assert.False(t, got.IsEnabled)
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, s.ID))
		got, err := repo.Get(t.Context(), tx, s.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
