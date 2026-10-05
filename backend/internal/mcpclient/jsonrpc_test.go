package mcpclient_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/mcpclient"
)

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func TestClient_Initialize(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req rpcEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-abc")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2025-03-26"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Bearer("test-token"))
	require.NoError(t, client.Initialize(t.Context()))
	assert.Equal(t, "Bearer test-token", gotAuth)
}

func TestClient_ListTools_FollowsPaginationCursor(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		calls++

		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"tools":      []map[string]string{{"name": "lookup_ip"}},
					"nextCursor": "page-2",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"tools": []map[string]string{{"name": "quarantine_host"}},
			},
		})
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Auth{})
	tools, err := client.ListTools(t.Context())
	require.NoError(t, err)
	require.Len(t, tools, 2)
	assert.Equal(t, "lookup_ip", tools[0].Name)
	assert.Equal(t, "quarantine_host", tools[1].Name)
	assert.Equal(t, 2, calls)
}

func TestClient_CallTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "tools/call", req.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"content": []map[string]string{{"type": "text", "text": "US"}},
			},
		})
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Auth{})
	result, err := client.CallTool(t.Context(), "lookup_ip", map[string]any{"ip": "10.0.0.5"})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "US", result.Content[0].Text)
}

func TestClient_CallTool_ServerReturnsRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"error": map[string]any{"code": -32601, "message": "method not found"},
		})
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Auth{})
	_, err := client.CallTool(t.Context(), "nonexistent_tool", nil)
	assert.ErrorContains(t, err, "method not found")
}

func TestClient_CallTool_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Auth{})
	_, err := client.CallTool(t.Context(), "lookup_ip", nil)
	assert.ErrorContains(t, err, "500")
}

func TestClient_CallTool_UnreachableServer(t *testing.T) {
	client := mcpclient.New("http://127.0.0.1:1", mcpclient.Auth{})
	_, err := client.CallTool(t.Context(), "lookup_ip", nil)
	assert.Error(t, err)
}

func TestClient_ServerSentEventsResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []map[string]string{{"type": "text", "text": "ok"}}},
		})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: " + string(payload) + "\n\n"))
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Auth{})
	result, err := client.CallTool(t.Context(), "lookup_ip", nil)
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "ok", result.Content[0].Text)
}

func TestClient_ResponseIDMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{}})
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Auth{})
	_, err := client.CallTool(t.Context(), "lookup_ip", nil)
	assert.ErrorContains(t, err, "does not match")
}

func TestClient_EmptyResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := mcpclient.New(srv.URL, mcpclient.Auth{})
	_, err := client.CallTool(t.Context(), "lookup_ip", nil)
	assert.ErrorContains(t, err, "empty response")
}

func TestClient_ListTools_ParsesAnnotations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + fmt.Sprint(req.ID) + `,"result":{"tools":[
			{"name":"reader","annotations":{"readOnlyHint":true}},
			{"name":"writer","annotations":{"readOnlyHint":false,"destructiveHint":true}},
			{"name":"plain"}
		]}}`))
	}))
	defer srv.Close()

	tools, err := mcpclient.New(srv.URL, mcpclient.Auth{}).ListTools(t.Context())

	require.NoError(t, err)
	require.Len(t, tools, 3)
	assert.True(t, tools[0].ReadOnly())
	assert.False(t, tools[1].ReadOnly(), "readOnlyHint false is not read-only")
	assert.False(t, tools[2].ReadOnly(), "no annotations means unknown, not read-only")
}
