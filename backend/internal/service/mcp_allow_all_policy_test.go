package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/mcpclient"
	"github.com/kuruops/kuruops/internal/service"
)

func boolPtr(b bool) *bool { return &b }

func tool(name string, ann *mcpclient.ToolAnnotations) mcpclient.Tool {
	return mcpclient.Tool{Name: name, Annotations: ann}
}

func TestEvaluateDiscoveredTool_AllowAll(t *testing.T) {
	server := domain.MCPServer{IsEnabled: true, AllowAllTools: true, SideEffectingTools: []string{"audited_read"}}
	readOnly := &mcpclient.ToolAnnotations{ReadOnlyHint: boolPtr(true)}

	cases := map[string]struct {
		tool         mcpclient.Tool
		wantApproval bool
	}{
		"a tool the server declares read-only runs without approval":   {tool("lookup_ip", readOnly), false},
		"a tool with no annotations is unknown, so it needs approval":  {tool("isolate_host", nil), true},
		"readOnlyHint false needs approval":                            {tool("delete_x", &mcpclient.ToolAnnotations{ReadOnlyHint: boolPtr(false)}), true},
		"annotations without a readOnlyHint need approval":             {tool("x", &mcpclient.ToolAnnotations{DestructiveHint: boolPtr(false)}), true},
		"a destructive tool needs approval":                            {tool("wipe", &mcpclient.ToolAnnotations{DestructiveHint: boolPtr(true)}), true},
		"read-only but listed as always-needs-approval still needs it": {tool("audited_read", readOnly), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			policy := service.EvaluateDiscoveredTool(server, tc.tool)
			assert.True(t, policy.Allowed, "allow-all offers every tool")
			assert.Equal(t, tc.wantApproval, policy.RequiresApproval)
		})
	}

	t.Run("a disabled server allows nothing", func(t *testing.T) {
		disabled := server
		disabled.IsEnabled = false
		assert.False(t, service.EvaluateDiscoveredTool(disabled, tool("lookup_ip", readOnly)).Allowed)
	})
}

func TestEvaluateDiscoveredTool_AllowListServerIgnoresAnnotations(t *testing.T) {
	server := domain.MCPServer{IsEnabled: true, AllowedTools: []string{"lookup_ip"}}
	readOnly := &mcpclient.ToolAnnotations{ReadOnlyHint: boolPtr(true)}

	assert.False(t, service.EvaluateDiscoveredTool(server, tool("other", readOnly)).Allowed,
		"a read-only hint must never widen an explicit allow-list")
	policy := service.EvaluateDiscoveredTool(server, tool("lookup_ip", nil))
	assert.True(t, policy.Allowed)
	assert.False(t, policy.RequiresApproval, "allow-list mode keeps its old rule: only the side-effecting list requires approval")
}

func TestEvaluateToolInvocation_AllowAllWithoutToolMetadataFailsSafe(t *testing.T) {
	server := domain.MCPServer{IsEnabled: true, AllowAllTools: true}
	policy := service.EvaluateToolInvocation(server, "anything")
	assert.True(t, policy.Allowed)
	assert.True(t, policy.RequiresApproval, "a bare name can't prove read-only, so it must never mean run unattended")
}
