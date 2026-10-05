package mcpclient_test

// Validates internal/mcpclient against a real, independently-implemented
// MCP server -- the package doc previously admitted "nothing here has been
// exercised against a live MCP server (none was available while building
// this)"; this closes that gap using the official reference server
// (@modelcontextprotocol/server-everything, streamableHttp transport):
//
//	npx -y @modelcontextprotocol/server-everything streamableHttp
//
// (see Taskfile.yml's mcp-reference:up / backend:test:mcp). Gated behind
// TEST_MCP_SERVER_URL the same way internal/secrets gates its Vault live
// test behind TEST_VAULT_ADDR -- the default backend:test:integration run
// shouldn't require a Node process to be up.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/mcpclient"
)

func skipUnlessMCPServerLive(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("TEST_MCP_SERVER_URL")
	if addr == "" {
		t.Skip("TEST_MCP_SERVER_URL not set -- run via `task backend:test:mcp`")
	}
	return addr
}

func TestMCPClient_Live_InitializeListToolsCallTool(t *testing.T) {
	addr := skipUnlessMCPServerLive(t)
	client := mcpclient.New(addr, mcpclient.Auth{})

	require.NoError(t, client.Initialize(t.Context()), "handshake against a real server")

	t.Run("ListTools finds the reference server's known tools", func(t *testing.T) {
		tools, err := client.ListTools(t.Context())
		require.NoError(t, err)
		require.NotEmpty(t, tools)

		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Name
		}
		assert.Contains(t, names, "echo", "the everything reference server always exposes an echo tool")
	})

	t.Run("CallTool invokes echo and gets a real response back", func(t *testing.T) {
		result, err := client.CallTool(t.Context(), "echo", map[string]any{"message": "hello from kuruops"})
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.Equal(t, "Echo: hello from kuruops", result.Content[0].Text)
	})

	t.Run("CallTool on an unknown tool name is a real server-side error, not a client crash", func(t *testing.T) {
		_, err := client.CallTool(t.Context(), "no-such-tool", nil)
		assert.Error(t, err)
	})
}
