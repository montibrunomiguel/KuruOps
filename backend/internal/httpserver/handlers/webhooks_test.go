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
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func newWebhookHandlerFixture(t *testing.T) (h *handlers.WebhookHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	h = handlers.NewWebhookHandlers(service.NewWebhookService(pool, repository.NewWebhookRepository(), repository.NewAdminAuditEventRepository()))
	return h, tenantID, actorID
}

func TestWebhookHandlers_CreateListRegenerateEnableDisable(t *testing.T) {
	h, tenantID, actorID := newWebhookHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create missing fields -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]string{"name": "Wazuh Prod", "source": "wazuh"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created["token"], "the plaintext token is returned exactly once, on create")
	endpoint := created["endpoint"].(map[string]any)
	id := endpoint["id"].(string)

	t.Run("list", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("regenerate", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+id+"/regenerate", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("disable then enable", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/"+id+"/disable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		req = withClaims(httptest.NewRequest("POST", "/"+id+"/enable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})
}

// TestWebhookHandlers_List_MissingTenantContext exercises the defensive
// "missing tenant context" 401 guard directly -- unreachable via a real
// request in production (JWTAuth always populates tenant context before a
// handler runs), but worth its own test since it's a distinct code path.
// A nil service is safe here: the guard returns before ever touching it.
func TestWebhookHandlers_ValidationErrors(t *testing.T) {
	h, tenantID, actorID := newWebhookHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("create invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("regenerate invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/regenerate", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("disable invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/disable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("enable invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/not-a-uuid/enable", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestWebhookHandlers_SetFieldMappingTemplate(t *testing.T) {
	h, tenantID, actorID := newWebhookHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"name": "Wazuh Prod", "source": "wazuh"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	id := created["endpoint"].(map[string]any)["id"].(string)

	pool := testutil.RequireTestDB(t)
	templateSvc := service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository())
	template, err := templateSvc.Create(t.Context(), tenantID, actorID, "Wazuh fields", nil)
	require.NoError(t, err)

	t.Run("invalid endpoint id -- 400", func(t *testing.T) {
		setBody, _ := json.Marshal(map[string]string{"templateId": template.ID.String()})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid/field-mapping-template", bytes.NewReader(setBody)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+id+"/field-mapping-template", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("assigns the template", func(t *testing.T) {
		setBody, _ := json.Marshal(map[string]string{"templateId": template.ID.String()})
		req := withClaims(httptest.NewRequest("PUT", "/"+id+"/field-mapping-template", bytes.NewReader(setBody)), tenantID, actorID, nil)
		require.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		listRec := doRequest(r, listReq)
		var endpoints []map[string]any
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &endpoints))
		require.Len(t, endpoints, 1)
		assert.Equal(t, template.ID.String(), endpoints[0]["fieldMappingTemplateId"])
	})

	t.Run("clears the template when templateId is null", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+id+"/field-mapping-template", bytes.NewReader([]byte(`{"templateId":null}`))), tenantID, actorID, nil)
		require.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		listRec := doRequest(r, listReq)
		var endpoints []map[string]any
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &endpoints))
		require.Len(t, endpoints, 1)
		assert.Nil(t, endpoints[0]["fieldMappingTemplateId"])
	})
}

func TestWebhookHandlers_SetGroupByFields(t *testing.T) {
	h, tenantID, actorID := newWebhookHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]string{"name": "Wazuh Prod", "source": "wazuh"})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	id := created["endpoint"].(map[string]any)["id"].(string)

	t.Run("invalid endpoint id -- 400", func(t *testing.T) {
		setBody, _ := json.Marshal(map[string]any{"groupByFields": []string{"host.name"}})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid/group-by-fields", bytes.NewReader(setBody)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+id+"/group-by-fields", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("assigns fields and a custom window", func(t *testing.T) {
		setBody, _ := json.Marshal(map[string]any{"groupByFields": []string{"host.name", "rule.id"}, "dedupWindowMinutes": 45})
		req := withClaims(httptest.NewRequest("PUT", "/"+id+"/group-by-fields", bytes.NewReader(setBody)), tenantID, actorID, nil)
		require.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		listRec := doRequest(r, listReq)
		var endpoints []map[string]any
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &endpoints))
		require.Len(t, endpoints, 1)
		assert.Equal(t, []any{"host.name", "rule.id"}, endpoints[0]["groupByFields"])
		assert.Equal(t, float64(45), endpoints[0]["dedupWindowMinutes"])
	})

	t.Run("empty groupByFields turns dedup back off", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+id+"/group-by-fields", bytes.NewReader([]byte(`{"groupByFields":[]}`))), tenantID, actorID, nil)
		require.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		listRec := doRequest(r, listReq)
		var endpoints []map[string]any
		require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &endpoints))
		require.Len(t, endpoints, 1)
		assert.Empty(t, endpoints[0]["groupByFields"])
	})
}

func TestWebhookHandlers_List_MissingTenantContext(t *testing.T) {
	h := handlers.NewWebhookHandlers(nil)
	r := newRouter(h.Routes)
	rec := doRequest(r, httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
