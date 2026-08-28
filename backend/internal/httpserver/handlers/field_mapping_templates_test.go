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

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func newFieldMappingTemplateHandlerFixture(t *testing.T) (h *handlers.FieldMappingTemplateHandlers, tenantID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	h = handlers.NewFieldMappingTemplateHandlers(service.NewFieldMappingTemplateService(pool, repository.NewFieldMappingTemplateRepository(), repository.NewAdminAuditEventRepository()))
	return h, tenantID, actorID
}

func TestFieldMappingTemplateHandlers_CreateListUpdateDelete(t *testing.T) {
	h, tenantID, actorID := newFieldMappingTemplateHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("empty name -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": ""})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("create invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	createBody, _ := json.Marshal(map[string]any{
		"name": "Wazuh fields",
		"rules": []map[string]string{
			{"jsonPath": "rule.level", "label": "Rule Level"},
			{"jsonPath": "", "label": "dropped -- blank path"},
			{"jsonPath": "agent.name", "label": ""},
		},
	})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(createBody)), tenantID, actorID, nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	var created domain.FieldMappingTemplate
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "Wazuh fields", created.Name)
	require.Len(t, created.Rules, 1, "rules with a blank path or label must be dropped, not stored blank")
	assert.Equal(t, "rule.level", created.Rules[0].JSONPath)

	t.Run("list includes it", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var list []domain.FieldMappingTemplate
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
		require.Len(t, list, 1)
		assert.Equal(t, created.ID, list[0].ID)
	})

	t.Run("update replaces name and rules", func(t *testing.T) {
		updateBody, _ := json.Marshal(map[string]any{
			"name":  "Wazuh fields v2",
			"rules": []map[string]string{{"jsonPath": "rule.groups", "label": "Categories"}},
		})
		req := withClaims(httptest.NewRequest("PUT", "/"+created.ID.String(), bytes.NewReader(updateBody)), tenantID, actorID, nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var updated domain.FieldMappingTemplate
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
		assert.Equal(t, "Wazuh fields v2", updated.Name)
		require.Len(t, updated.Rules, 1)
		assert.Equal(t, "Categories", updated.Rules[0].Label)
	})

	t.Run("update invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid", bytes.NewReader(createBody)), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("delete invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, actorID, nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	req = withClaims(httptest.NewRequest("DELETE", "/"+created.ID.String(), nil), tenantID, actorID, nil)
	rec = doRequest(r, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestFieldMappingTemplateHandlers_List_MissingTenantContext(t *testing.T) {
	h := handlers.NewFieldMappingTemplateHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
