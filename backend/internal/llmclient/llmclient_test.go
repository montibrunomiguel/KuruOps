package llmclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/llmclient"
)

func TestNew_UnknownKind(t *testing.T) {
	_, err := llmclient.New("made_up", nil, "key", "model")
	assert.ErrorContains(t, err, "unknown llm provider kind")
}

func TestNew_OpenAICompatibleRequiresBaseURL(t *testing.T) {
	_, err := llmclient.New("openai_compatible", nil, "key", "model")
	assert.ErrorContains(t, err, "requires a base_url")
}

func TestOpenAIClient_Complete(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"looks like a brute-force attempt"}}]}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	c, err := llmclient.New("openai_compatible", &baseURL, "sk-test", "gpt-4o")
	require.NoError(t, err)

	result, err := c.Complete(t.Context(), "You are a SOC analyst.", "Analyze this alert.")
	require.NoError(t, err)
	assert.Equal(t, "looks like a brute-force attempt", result)
	assert.Equal(t, "Bearer sk-test", gotAuth)
	assert.Equal(t, "/chat/completions", gotPath)
	assert.Equal(t, "gpt-4o", gotBody["model"])
}

func TestOpenAIClient_Complete_ProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	c, err := llmclient.New("self_hosted", &baseURL, "bad-key", "llama3")
	require.NoError(t, err)

	_, err = c.Complete(t.Context(), "sys", "user")
	assert.ErrorContains(t, err, "invalid api key")
}

func TestOpenAIClient_CompleteWithTools_RequestsAndReceivesToolCalls(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup_ip","arguments":"{\"ip\":\"10.0.0.5\"}"}}]}}]}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	c, err := llmclient.New("openai_compatible", &baseURL, "sk-test", "gpt-4o")
	require.NoError(t, err)

	result, err := c.CompleteWithTools(t.Context(), "sys", []llmclient.Message{{Role: llmclient.RoleUser, Content: "check this ip"}}, []llmclient.Tool{
		{Name: "lookup_ip", Description: "look up ip reputation", InputSchema: json.RawMessage(`{"type":"object"}`)},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Text)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "call_1", result.ToolCalls[0].ID)
	assert.Equal(t, "lookup_ip", result.ToolCalls[0].Name)
	assert.Equal(t, "10.0.0.5", result.ToolCalls[0].Args["ip"])

	// verify the wire request shape: system message first, tools sent, one function tool
	messages, _ := gotBody["messages"].([]any)
	require.NotEmpty(t, messages)
	first := messages[0].(map[string]any)
	assert.Equal(t, "system", first["role"])
	tools, _ := gotBody["tools"].([]any)
	require.Len(t, tools, 1)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	assert.Equal(t, "lookup_ip", fn["name"])
}

func TestOpenAIClient_CompleteWithTools_RoundTripsToolResultMessage(t *testing.T) {
	var gotMessages []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotMessages = body.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"clean"}}]}`))
	}))
	defer srv.Close()

	baseURL := srv.URL
	c, err := llmclient.New("openai_compatible", &baseURL, "sk-test", "gpt-4o")
	require.NoError(t, err)

	_, err = c.CompleteWithTools(t.Context(), "sys", []llmclient.Message{
		{Role: llmclient.RoleUser, Content: "check this ip"},
		{Role: llmclient.RoleAssistant, ToolCalls: []llmclient.ToolCall{{ID: "call_1", Name: "lookup_ip", Args: map[string]any{"ip": "10.0.0.5"}}}},
		{Role: llmclient.RoleTool, ToolCallID: "call_1", Content: `{"reputation":"clean"}`},
	}, nil)
	require.NoError(t, err)

	// system + user + assistant(tool_calls) + tool
	require.Len(t, gotMessages, 4)
	assert.Equal(t, "tool", gotMessages[3]["role"])
	assert.Equal(t, "call_1", gotMessages[3]["tool_call_id"])
	assistantMsg := gotMessages[2]
	assert.Equal(t, "assistant", assistantMsg["role"])
	toolCalls, _ := assistantMsg["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
}

func TestOpenAIClient_Complete_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	baseURL := srv.URL
	c, err := llmclient.New("azure_openai", &baseURL, "key", "model")
	require.NoError(t, err)

	_, err = c.Complete(t.Context(), "sys", "user")
	assert.ErrorContains(t, err, "500")
}
