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

func newLLMProviderHandlerFixture(t *testing.T) (h *handlers.LLMProviderHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	h = handlers.NewLLMProviderHandlers(service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), secrets.NewEnvStore(), repository.NewAdminAuditEventRepository()))
	return h, tenantID, actorID
}

func TestLLMProviderHandlers_CreateListUpdateSetDefaultDelete(t *testing.T) {
	h, tenantID, actorID := newLLMProviderHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create missing required fields -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]string{"name": "OpenAI", "kind": "openai_compatible", "model": "gpt-4o", "apiKey": "sk-test"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var p map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	id := p["id"].(string)
	assert.NotContains(t, rec.Body.String(), "sk-test", "the api key is never echoed back")

	t.Run("list", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("update", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "OpenAI v2", "kind": "openai_compatible", "model": "gpt-4o-mini"})
		req := withClaims(httptest.NewRequest("PUT", "/"+id, bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusOK, doRequest(r, req).Code)
	})

	t.Run("set default", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+id+"/default", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("delete", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/"+id, nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

func TestLLMProviderHandlers_ValidationAndNotFound(t *testing.T) {
	h, tenantID, actorID := newLLMProviderHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("create unknown kind -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "Bad", "kind": "bogus", "model": "gpt-4o", "apiKey": "sk-test"})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update invalid id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "OpenAI", "kind": "openai_compatible", "model": "gpt-4o"})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+uuid.New().String(), bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update unknown provider -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "OpenAI", "kind": "openai_compatible", "model": "gpt-4o"})
		req := withClaims(httptest.NewRequest("PUT", "/"+uuid.New().String(), bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("setDefault invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/default", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestLLMProviderHandlers_List_MissingTenantContext(t *testing.T) {
	h := handlers.NewLLMProviderHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
