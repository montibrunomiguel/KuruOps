package mcpclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/mcpclient"
)

// initializeServer answers the MCP handshake and records the headers of every
// request it receives.
func initializeServer(t *testing.T, seen *[]http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Header.Clone())
		var req rpcEnvelope
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		if req.Method == "initialize" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"protocolVersion": "2025-03-26"},
			})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
}

func TestClient_AuthHeader(t *testing.T) {
	t.Run("api key is sent in the configured header, not Authorization", func(t *testing.T) {
		var seen []http.Header
		srv := initializeServer(t, &seen)
		defer srv.Close()

		require.NoError(t, mcpclient.New(srv.URL, mcpclient.Header("X-Api-Key", "k-123")).Initialize(t.Context()))

		require.NotEmpty(t, seen)
		for _, h := range seen {
			assert.Equal(t, "k-123", h.Get("X-Api-Key"))
			assert.Empty(t, h.Get("Authorization"))
		}
	})

	t.Run("no auth sends no credential header", func(t *testing.T) {
		var seen []http.Header
		srv := initializeServer(t, &seen)
		defer srv.Close()

		require.NoError(t, mcpclient.New(srv.URL, mcpclient.Auth{}).Initialize(t.Context()))

		for _, h := range seen {
			assert.Empty(t, h.Get("Authorization"))
			assert.Empty(t, h.Get("X-Api-Key"))
		}
	})
}

func TestBearerAndHeaderConstructors_EmptyInputIsNoAuth(t *testing.T) {
	assert.Equal(t, mcpclient.Auth{}, mcpclient.Bearer(""))
	assert.Equal(t, mcpclient.Auth{}, mcpclient.Header("X-Api-Key", ""))
	assert.Equal(t, mcpclient.Auth{}, mcpclient.Header("", "v"))
}

func TestClient_RefusesCrossHostRedirectWithoutForwardingCredential(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "" {
			leaked = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	// httptest servers share 127.0.0.1 and differ only by port, which is
	// still a different Host.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	err := mcpclient.New(redirector.URL, mcpclient.Header("X-Api-Key", "secret")).Initialize(t.Context())

	require.Error(t, err)
	assert.False(t, leaked, "the API key must not be replayed to the redirect target")
}

func TestValidateHeaderName(t *testing.T) {
	valid := []string{"X-Api-Key", "x-api-key", "Api_Key", "X-Custom.Header", "Ocp-Apim-Subscription-Key", "Authorization"}
	for _, name := range valid {
		assert.NoError(t, mcpclient.ValidateHeaderName(name), name)
	}

	invalid := map[string]string{
		"empty":             "",
		"space":             "X Api Key",
		"colon":             "X-Api-Key:",
		"newline injection": "X-Key\r\nX-Evil: 1",
		"non-ascii":         "X-Ключ",
		"reserved host":     "Host",
		"reserved mixed":    "Content-Length",
		"reserved session":  "mcp-session-id",
		"hop by hop":        "Transfer-Encoding",
		"too long":          strings.Repeat("a", 129),
	}
	for name, header := range invalid {
		assert.Error(t, mcpclient.ValidateHeaderName(header), name)
	}
}

func TestValidateCredential(t *testing.T) {
	assert.NoError(t, mcpclient.ValidateCredential("sk-live_abc.DEF~123"))
	assert.NoError(t, mcpclient.ValidateCredential("has spaces inside is fine"))

	assert.Error(t, mcpclient.ValidateCredential(""))
	assert.Error(t, mcpclient.ValidateCredential("token\nX-Injected: 1"))
	assert.Error(t, mcpclient.ValidateCredential("token\r"))
	assert.Error(t, mcpclient.ValidateCredential("tab\there"))
	assert.Error(t, mcpclient.ValidateCredential("nul\x00byte"))
	assert.Error(t, mcpclient.ValidateCredential("del\x7f"))
	assert.Error(t, mcpclient.ValidateCredential(strings.Repeat("a", 4097)))
}
