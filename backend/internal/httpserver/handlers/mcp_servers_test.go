package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newMCPServerHandlerFixture(t *testing.T) (h *handlers.MCPServerHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewMCPServerService(pool, repository.NewMCPServerRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository())
	toolSvc := service.NewMCPToolService(pool, repository.NewMCPServerRepository(), repository.NewAIToolCallRepository(), secrets.NewEnvStore())
	h = handlers.NewMCPServerHandlers(svc, toolSvc)
	return h, tenantID, actorID
}

func TestMCPServerHandlers_CreateListUpdateEnableDisableDelete(t *testing.T) {
	h, tenantID, actorID := newMCPServerHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create missing fields -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("side-effecting tool not in allow-list -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"name": "Threat Intel", "transport": "http", "endpointOrCommand": "https://mcp.example.com",
			"allowedTools": []string{"lookup_ip"}, "sideEffectingTools": []string{"quarantine_host"},
		})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]any{
		"name": "Threat Intel", "transport": "http", "endpointOrCommand": "https://mcp.example.com",
		"allowedTools": []string{"lookup_ip"},
	})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var srv map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &srv))
	id := srv["id"].(string)

	t.Run("list", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("update", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"name": "Threat Intel v2", "transport": "http", "endpointOrCommand": "https://mcp.example.com",
			"allowedTools": []string{"lookup_ip", "lookup_domain"},
		})
		req := withClaims(httptest.NewRequest("PUT", "/"+id, bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("disable then enable", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+id+"/disable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
		req = withClaims(httptest.NewRequest("POST", "/"+id+"/enable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("discover-tools without a reachable server -- 502", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+id+"/discover-tools", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadGateway, doRequest(r, req).Code)
	})

	t.Run("delete", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+id, nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

func TestMCPServerHandlers_ToolCallApprovals(t *testing.T) {
	h, tenantID, actorID := newMCPServerHandlerFixture(t)
	r := newRouter(h.Routes)

	req := withClaims(httptest.NewRequest("GET", "/tool-calls", nil), tenantID, actorID, nil)
	assert.Equal(t, http.StatusOK, doRequest(r, req).Code)

	t.Run("approve unknown call id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/tool-calls/999999/approve", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("malformed call id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/tool-calls/not-a-number/reject", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestMCPServerHandlers_ValidationAndNotFound(t *testing.T) {
	h, tenantID, actorID := newMCPServerHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update invalid id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "X", "transport": "http", "endpointOrCommand": "https://example.com"})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+uuid.New().String(), bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update unknown server -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "X", "transport": "http", "endpointOrCommand": "https://example.com"})
		req := withClaims(httptest.NewRequest("PUT", "/"+uuid.New().String(), bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("enable invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/enable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("disable invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/disable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("discover-tools invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/discover-tools", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("approve malformed call id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/tool-calls/not-a-number/approve", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("reject unknown call id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/tool-calls/999999/reject", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestMCPServerHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewMCPServerHandlers(nil, nil)
	r := newRouter(h.Routes)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/"}, {"GET", "/tool-calls"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
	}
}
