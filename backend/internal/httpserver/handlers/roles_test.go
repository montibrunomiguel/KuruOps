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

func newRoleHandlerFixture(t *testing.T) (h *handlers.RoleHandlers, tenantID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	svc := service.NewRoleService(pool, repository.NewRoleRepository(), repository.NewAdminAuditEventRepository())
	return handlers.NewRoleHandlers(svc), tenantID
}

func TestRoleHandlers_CreateListUpdate(t *testing.T) {
	h, tenantID := newRoleHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("missing name -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"resourceAccess": []string{"alerts"}})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]any{"name": "Analyst", "resourceAccess": []string{"alerts", "incidents"}})
	req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	rec := doRequest(r, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created domain.Role
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "Analyst", created.Name)
	assert.False(t, created.IsAdmin)

	listReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
	listRec := doRequest(r, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)
	var list []domain.Role
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	assert.Equal(t, created.ID, list[0].ID)

	t.Run("update -- 200 with the new values", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "Senior Analyst", "isAdmin": true, "resourceAccess": []string{"alerts", "incidents", "followup"}})
		req := withClaims(httptest.NewRequest("PUT", "/"+created.ID.String(), bytes.NewReader(body)), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var updated domain.Role
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
		assert.Equal(t, "Senior Analyst", updated.Name)
		assert.True(t, updated.IsAdmin)
	})

	t.Run("update invalid id -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "X", "resourceAccess": []string{}})
		req := withClaims(httptest.NewRequest("PUT", "/not-a-uuid", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("update invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/"+created.ID.String(), bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestRoleHandlers_Delete(t *testing.T) {
	h, tenantID := newRoleHandlerFixture(t)
	r := newRouter(h.Routes)

	t.Run("invalid id -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/not-a-uuid", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("deletes an unused role -- 204", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "Unused", "resourceAccess": []string{"alerts"}})
		req := withClaims(httptest.NewRequest("POST", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		var created domain.Role
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))

		delReq := withClaims(httptest.NewRequest("DELETE", "/"+created.ID.String(), nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, delReq).Code)
	})

	t.Run("refuses to delete a role still assigned to a user -- 409", func(t *testing.T) {
		roleID := testutil.NewRole(t, tenantID, false, []string{"alerts"})
		testutil.NewUserWithRole(t, tenantID, roleID)

		req := withClaims(httptest.NewRequest("DELETE", "/"+roleID.String(), nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusConflict, doRequest(r, req).Code)
	})
}

func TestRoleHandlers_List_MissingTenantContext(t *testing.T) {
	h, _ := newRoleHandlerFixture(t)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}

func TestRoleHandlers_Create_MissingTenantContext(t *testing.T) {
	h, _ := newRoleHandlerFixture(t)
	r := newRouter(h.Routes)

	body, _ := json.Marshal(map[string]any{"name": "X", "resourceAccess": []string{}})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
