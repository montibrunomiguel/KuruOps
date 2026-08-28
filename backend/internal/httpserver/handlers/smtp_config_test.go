package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/httpserver/handlers"
	"github.com/kuruops/kuruops/internal/mailer"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// noopSender is a mailer.Sender that always succeeds without touching the
// network -- these handler tests only need to prove routing/wiring, not
// mailer delivery mechanics (covered by internal/mailer's own tests).
type noopSender struct{}

func (noopSender) Send(context.Context, mailer.Config, mailer.Message) error { return nil }

func TestSMTPConfigHandlers(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), noopSender{}, repository.NewAdminAuditEventRepository())
	h := handlers.NewSMTPConfigHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("get before any config -- 200 with null body", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "null\n", rec.Body.String())
	})

	t.Run("save without a host -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"port": 587, "fromAddress": "a@example.com"})
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("test email before config exists -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"to": "someone@example.com"})
		req := withClaims(httptest.NewRequest("POST", "/test", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("save invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("test email invalid JSON body -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("POST", "/test", bytes.NewReader([]byte("{not-json"))), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	t.Run("test email missing to -- 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"to": ""})
		req := withClaims(httptest.NewRequest("POST", "/test", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})

	saveBody, _ := json.Marshal(map[string]any{
		"host": "smtp.example.com", "port": 587, "useTls": true,
		"username": "smtp-user", "password": "s3cret",
		"fromAddress": "no-reply@example.com", "fromName": "KuruOps",
	})
	saveReq := withClaims(httptest.NewRequest("PUT", "/", bytes.NewReader(saveBody)), tenantID, uuid.New(), nil)
	assert.Equal(t, http.StatusNoContent, doRequest(r, saveReq).Code)

	t.Run("get after save reflects the config but never the password", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "s3cret")
		assert.Contains(t, rec.Body.String(), `"host":"smtp.example.com"`)
	})

	t.Run("send test email now that config exists", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"to": "someone@example.com"})
		req := withClaims(httptest.NewRequest("POST", "/test", bytes.NewReader(body)), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)
	})

	t.Run("delete turns email sending off", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("DELETE", "/", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusNoContent, doRequest(r, req).Code)

		getReq := withClaims(httptest.NewRequest("GET", "/", nil), tenantID, uuid.New(), nil)
		getRec := doRequest(r, getReq)
		assert.Equal(t, "null\n", getRec.Body.String())
	})
}

func TestSMTPConfigHandlers_MissingTenantContext(t *testing.T) {
	h := handlers.NewSMTPConfigHandlers(nil)
	r := newRouter(h.Routes)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/"}, {"DELETE", "/"}, {"POST", "/test"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
		})
	}
}
