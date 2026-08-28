package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestRetentionConfigHandlers(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewRetentionConfigService(pool, repository.NewRetentionConfigRepository(), repository.NewAdminAuditEventRepository())
	h := handlers.NewRetentionConfigHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("get before any config -- 200 with the synthesized 18-month default, not null", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"alertRetentionMonths":18`)
		assert.Contains(t, rec.Body.String(), `"incidentRetentionMonths":18`)
		assert.Contains(t, rec.Body.String(), `"configured":false`)
	})

	t.Run("save negative months -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]int{"alertRetentionMonths": -1, "incidentRetentionMonths": 18})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	body, _ := json.Marshal(map[string]int{"alertRetentionMonths": 6, "incidentRetentionMonths": 36})
	req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

	t.Run("get after save reflects the saved values and configured=true", func(t *testing.T) {
		getReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, getReq)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"alertRetentionMonths":6`)
		assert.Contains(t, rec.Body.String(), `"incidentRetentionMonths":36`)
		assert.Contains(t, rec.Body.String(), `"configured":true`)
	})
}

func TestRetentionConfigHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewRetentionConfigHandlers(nil)
	r := newRouter(h.Routes)

	for _, tc := range []struct{ method, path string }{{"GET", "/"}, {"PUT", "/"}} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
	}
}
