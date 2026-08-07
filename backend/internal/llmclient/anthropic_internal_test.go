package llmclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnthropicClient_Complete lives in-package (not llmclient_test) so it
// can redirect anthropicAPIURL at a local server -- the client always dials
// the real Anthropic API otherwise, per anthropicAPIURL's doc comment.
func TestAnthropicClient_Complete(t *testing.T) {
	var gotAPIKey, gotVersion string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"escalate immediately"}]}`))
	}))
	defer srv.Close()

	original := anthropicAPIURL
	anthropicAPIURL = srv.URL
	t.Cleanup(func() { anthropicAPIURL = original })

	c, err := New("anthropic", nil, "anthropic-key", "claude-opus-4")
	require.NoError(t, err)

	result, err := c.Complete(t.Context(), "You are a SOC analyst.", "Analyze this incident.")
	require.NoError(t, err)
	assert.Equal(t, "escalate immediately", result)
	assert.Equal(t, "anthropic-key", gotAPIKey)
	assert.Equal(t, anthropicAPIVersion, gotVersion)
	assert.Equal(t, "claude-opus-4", gotBody["model"])
	assert.Equal(t, "You are a SOC analyst.", gotBody["system"])
}

func TestAnthropicClient_CompleteWithTools_RequestsAndReceivesToolCalls(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"let me check that"},{"type":"tool_use","id":"toolu_1","name":"lookup_ip","input":{"ip":"10.0.0.5"}}]}`))
	}))
	defer srv.Close()

	original := anthropicAPIURL
	anthropicAPIURL = srv.URL
	t.Cleanup(func() { anthropicAPIURL = original })

	c, err := New("anthropic", nil, "anthropic-key", "claude-opus-4")
	require.NoError(t, err)

	result, err := c.CompleteWithTools(t.Context(), "sys", []Message{{Role: RoleUser, Content: "check this ip"}}, []Tool{
		{Name: "lookup_ip", Description: "look up ip reputation", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	require.NoError(t, err)
	assert.Equal(t, "let me check that", result.Text)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "toolu_1", result.ToolCalls[0].ID)
	assert.Equal(t, "lookup_ip", result.ToolCalls[0].Name)
	assert.Equal(t, "10.0.0.5", result.ToolCalls[0].Args["ip"])

	tools, _ := gotBody["tools"].([]any)
	require.Len(t, tools, 1)
	assert.Equal(t, "lookup_ip", tools[0].(map[string]any)["name"])
}

func TestAnthropicClient_CompleteWithTools_RoundTripsToolResultAsUserMessage(t *testing.T) {
	var gotMessages []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotMessages = body.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"clean"}]}`))
	}))
	defer srv.Close()

	original := anthropicAPIURL
	anthropicAPIURL = srv.URL
	t.Cleanup(func() { anthropicAPIURL = original })

	c, err := New("anthropic", nil, "key", "model")
	require.NoError(t, err)

	_, err = c.CompleteWithTools(t.Context(), "sys", []Message{
		{Role: RoleUser, Content: "check this ip"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "lookup_ip", Args: map[string]any{"ip": "10.0.0.5"}}}},
		{Role: RoleTool, ToolCallID: "toolu_1", Content: `{"reputation":"clean"}`},
	}, nil)
	require.NoError(t, err)

	require.Len(t, gotMessages, 3)
	// the tool result lands as a "user" message with a tool_result content
	// block -- Anthropic has no native "tool" role, unlike OpenAI.
	toolResultMsg := gotMessages[2]
	assert.Equal(t, "user", toolResultMsg["role"])
	blocks, _ := toolResultMsg["content"].([]any)
	require.Len(t, blocks, 1)
	block := blocks[0].(map[string]any)
	assert.Equal(t, "tool_result", block["type"])
	assert.Equal(t, "toolu_1", block["tool_use_id"])
}

func TestAnthropicClient_Complete_NoTextContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[]}`))
	}))
	defer srv.Close()

	original := anthropicAPIURL
	anthropicAPIURL = srv.URL
	t.Cleanup(func() { anthropicAPIURL = original })

	c, err := New("anthropic", nil, "key", "model")
	require.NoError(t, err)

	_, err = c.Complete(t.Context(), "sys", "user")
	assert.ErrorContains(t, err, "no text content")
}
