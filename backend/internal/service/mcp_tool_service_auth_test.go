package service_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// mcpStub is a minimal MCP server that records the credential headers it sees.
type mcpStub struct {
	*httptest.Server
	mu      sync.Mutex
	headers []http.Header
}

func newMCPStub(t *testing.T) *mcpStub {
	t.Helper()
	s := &mcpStub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.headers = append(s.headers, r.Header.Clone())
		s.mu.Unlock()
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"protocolVersion": "2025-03-26"}})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []map[string]string{{"name": "lookup_ip"}}}})
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *mcpStub) first() http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.headers) == 0 {
		return http.Header{}
	}
	return s.headers[0]
}

func TestMCPToolService_Dial_SendsTheConfiguredCredential(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	store := secrets.NewEnvStore()
	servers := repository.NewMCPServerRepository()
	mcpSvc := service.NewMCPServerService(pool, servers, store, repository.NewAdminAuditEventRepository())
	toolSvc := service.NewMCPToolService(pool, servers, repository.NewAIToolCallRepository(), store)

	create := func(t *testing.T, stub *mcpStub, name string, auth service.MCPServerAuthInput) *domain.MCPServer {
		t.Helper()
		srv, err := mcpSvc.Create(t.Context(), tenantID, actorID, service.MCPServerSaveInput{
			Name: name, Transport: "http", EndpointOrCommand: stub.URL, Auth: auth,
		})
		require.NoError(t, err)
		return srv
	}

	t.Run("none sends no credential", func(t *testing.T) {
		stub := newMCPStub(t)
		srv := create(t, stub, "none", service.MCPServerAuthInput{})
		_, err := toolSvc.DiscoverTools(t.Context(), tenantID, srv.ID)
		require.NoError(t, err)
		assert.Empty(t, stub.first().Get("Authorization"))
	})

	t.Run("api key goes in the configured header", func(t *testing.T) {
		stub := newMCPStub(t)
		srv := create(t, stub, "api-key", service.MCPServerAuthInput{Type: domain.MCPAuthAPIKey, APIKeyHeader: "X-Api-Key", APIKey: "k-777"})
		_, err := toolSvc.DiscoverTools(t.Context(), tenantID, srv.ID)
		require.NoError(t, err)
		assert.Equal(t, "k-777", stub.first().Get("X-Api-Key"))
		assert.Empty(t, stub.first().Get("Authorization"))
	})

	t.Run("bearer goes in Authorization", func(t *testing.T) {
		stub := newMCPStub(t)
		srv := create(t, stub, "bearer", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "tok-9"})
		_, err := toolSvc.DiscoverTools(t.Context(), tenantID, srv.ID)
		require.NoError(t, err)
		assert.Equal(t, "Bearer tok-9", stub.first().Get("Authorization"))
	})

	t.Run("oauth fetches a client_credentials token once and reuses it", func(t *testing.T) {
		var tokenCalls atomic.Int32
		var gotUser, gotPass string
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenCalls.Add(1)
			gotUser, gotPass, _ = r.BasicAuth()
			_ = r.ParseForm()
			assert.Equal(t, "client_credentials", r.PostForm.Get("grant_type"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"minted-token","token_type":"Bearer","expires_in":3600}`))
		}))
		defer authServer.Close()

		stub := newMCPStub(t)
		srv := create(t, stub, "oauth", service.MCPServerAuthInput{
			Type: domain.MCPAuthOAuth, OAuthTokenURL: authServer.URL, OAuthClientID: "cid", OAuthClientSecret: "csecret",
		})

		for range 2 {
			_, err := toolSvc.DiscoverTools(t.Context(), tenantID, srv.ID)
			require.NoError(t, err)
		}

		assert.Equal(t, "Bearer minted-token", stub.first().Get("Authorization"))
		assert.Equal(t, "cid", gotUser)
		assert.Equal(t, "csecret", gotPass)
		assert.Equal(t, int32(1), tokenCalls.Load(), "the access token is cached between calls")
	})

	t.Run("oauth token endpoint failure is an error that carries no secret", func(t *testing.T) {
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
		}))
		defer authServer.Close()

		stub := newMCPStub(t)
		srv := create(t, stub, "oauth-bad", service.MCPServerAuthInput{
			Type: domain.MCPAuthOAuth, OAuthTokenURL: authServer.URL, OAuthClientID: "cid", OAuthClientSecret: "csecret-leak-check",
		})

		_, err := toolSvc.DiscoverTools(t.Context(), tenantID, srv.ID)

		require.Error(t, err)
		assert.ErrorContains(t, err, "invalid_client")
		assert.NotContains(t, err.Error(), "csecret-leak-check")
		assert.Empty(t, stub.first(), "the MCP server is never contacted without a token")
	})

	t.Run("a credential that can't be resolved fails closed instead of connecting unauthenticated", func(t *testing.T) {
		stub := newMCPStub(t)
		srv := create(t, stub, "lost-secret", service.MCPServerAuthInput{Type: domain.MCPAuthBearer, BearerToken: "tok"})

		// A tool service backed by an empty store: Resolve returns "" with no
		// error for an unknown ref, exactly like a wiped/rotated secret backend.
		emptyStore := secrets.NewEnvStore()
		blind := service.NewMCPToolService(pool, servers, repository.NewAIToolCallRepository(), emptyStore)

		_, err := blind.DiscoverTools(t.Context(), tenantID, srv.ID)

		require.Error(t, err)
		assert.ErrorContains(t, err, "could not be resolved")
		assert.Empty(t, stub.first(), "no request may go out without the configured credential")
	})
}
