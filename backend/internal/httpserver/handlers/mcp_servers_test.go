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

	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
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

func TestMCPServerHandlers_Authentication(t *testing.T) {
	h, tenantID, actorID := newMCPServerHandlerFixture(t)
	r := newRouter(h.Routes)

	post := func(t *testing.T, payload map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		payload["transport"] = "http"
		if _, ok := payload["endpointOrCommand"]; !ok {
			payload["endpointOrCommand"] = "https://mcp.example.com"
		}
		body, _ := json.Marshal(payload)
		return doRequest(r, withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil))
	}

	cases := map[string]struct {
		payload map[string]any
		secret  string
		check   func(t *testing.T, srv map[string]any)
	}{
		"none by default": {
			payload: map[string]any{"name": "a-none"},
			check:   func(t *testing.T, srv map[string]any) { assert.Equal(t, "none", srv["authType"]) },
		},
		"api key": {
			payload: map[string]any{"name": "a-key", "authType": "api_key", "apiKeyHeader": "X-Api-Key", "apiKey": "super-secret-key"},
			secret:  "super-secret-key",
			check: func(t *testing.T, srv map[string]any) {
				assert.Equal(t, "api_key", srv["authType"])
				assert.Equal(t, "X-Api-Key", srv["authHeaderName"])
			},
		},
		"bearer": {
			payload: map[string]any{"name": "a-bearer", "authType": "bearer", "bearerToken": "super-secret-token"},
			secret:  "super-secret-token",
			check:   func(t *testing.T, srv map[string]any) { assert.Equal(t, "bearer", srv["authType"]) },
		},
		"oauth": {
			payload: map[string]any{
				"name": "a-oauth", "authType": "oauth", "oauthTokenUrl": "https://auth.example.com/token",
				"oauthClientId": "my-client", "oauthClientSecret": "super-secret-client-secret",
			},
			secret: "super-secret-client-secret",
			check: func(t *testing.T, srv map[string]any) {
				assert.Equal(t, "oauth", srv["authType"])
				assert.Equal(t, "https://auth.example.com/token", srv["oauthTokenUrl"])
				assert.Equal(t, "my-client", srv["oauthClientId"])
			},
		},
	}
	for name, tc := range cases {
		t.Run(name+" -- 201 and no secret in the response", func(t *testing.T) {
			rec := post(t, tc.payload)
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
			if tc.secret != "" {
				assert.NotContains(t, rec.Body.String(), tc.secret)
			}
			var srv map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &srv))
			tc.check(t, srv)
		})
	}

	t.Run("the list endpoint never leaks a secret either", func(t *testing.T) {
		rec := doRequest(r, withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		for _, tc := range cases {
			if tc.secret != "" {
				assert.NotContains(t, rec.Body.String(), tc.secret)
			}
		}
		assert.NotContains(t, rec.Body.String(), "SecretRef")
		assert.NotContains(t, rec.Body.String(), "secretRef")
	})

	t.Run("the legacy authToken field is rejected, not silently dropped", func(t *testing.T) {
		rec := post(t, map[string]any{"name": "legacy", "authToken": "old-style"})
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "authToken")
		assert.NotContains(t, rec.Body.String(), "old-style")
	})

	t.Run("an invalid combination is a 400 that doesn't echo the secret", func(t *testing.T) {
		rec := post(t, map[string]any{"name": "bad", "authType": "api_key", "apiKeyHeader": "Host", "apiKey": "echo-me-not"})
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.NotContains(t, rec.Body.String(), "echo-me-not")
	})

	t.Run("update without authType keeps authentication (the Discover Tools save)", func(t *testing.T) {
		rec := post(t, map[string]any{"name": "keeps-auth", "authType": "bearer", "bearerToken": "tok-keep"})
		require.Equal(t, http.StatusCreated, rec.Code)
		var srv map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &srv))

		body, _ := json.Marshal(map[string]any{
			"name": "keeps-auth", "transport": "http", "endpointOrCommand": "https://mcp.example.com",
			"allowedTools": []string{"lookup_ip"},
		})
		req := withClaims(httptest.NewRequest("PUT", "/"+srv["id"].(string), bytes.NewReader(body)), tenantID, actorID, nil)
		rec = doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var updated map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
		assert.Equal(t, "bearer", updated["authType"])
	})
}

func TestMCPServerHandlers_AllowAllTools(t *testing.T) {
	h, tenantID, actorID := newMCPServerHandlerFixture(t)
	r := newRouter(h.Routes)

	send := func(t *testing.T, method, path string, payload map[string]any) (int, map[string]any) {
		t.Helper()
		body, _ := json.Marshal(payload)
		rec := doRequest(r, withClaims(httptest.NewRequest(method, path, bytes.NewReader(body)), tenantID, actorID, nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	base := func(name string) map[string]any {
		return map[string]any{"name": name, "transport": "http", "endpointOrCommand": "https://mcp.example.com"}
	}

	t.Run("create with allowAllTools true -- reflected in the response, no allow-list needed", func(t *testing.T) {
		payload := base("all")
		payload["allowAllTools"] = true
		payload["sideEffectingTools"] = []string{"isolate_host"}
		code, srv := send(t, "POST", "/", payload)
		require.Equal(t, http.StatusCreated, code)
		assert.Equal(t, true, srv["allowAllTools"])
	})

	t.Run("omitting it on create means an explicit allow-list", func(t *testing.T) {
		code, srv := send(t, "POST", "/", base("default"))
		require.Equal(t, http.StatusCreated, code)
		assert.Equal(t, false, srv["allowAllTools"])
	})

	t.Run("a PUT that omits it leaves the mode alone; an explicit false changes it", func(t *testing.T) {
		payload := base("flip")
		payload["allowAllTools"] = true
		_, srv := send(t, "POST", "/", payload)
		id := srv["id"].(string)

		code, got := send(t, "PUT", "/"+id, base("flip"))
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, true, got["allowAllTools"], "the Discover Tools save omits the field and must not reset it")

		off := base("flip")
		off["allowAllTools"] = false
		code, got = send(t, "PUT", "/"+id, off)
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, false, got["allowAllTools"])
	})

	t.Run("allow-list mode still rejects a side-effecting tool that is not allow-listed", func(t *testing.T) {
		payload := base("strict")
		payload["allowAllTools"] = false
		payload["allowedTools"] = []string{"lookup_ip"}
		payload["sideEffectingTools"] = []string{"isolate_host"}
		code, _ := send(t, "POST", "/", payload)
		assert.Equal(t, http.StatusBadRequest, code)
	})
}
