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

	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// noopSender is a mailer.Sender that always succeeds without touching the
// network -- these handler tests only need to prove routing/wiring, not
// mailer delivery mechanics (covered by internal/mailer's own tests).
type noopSender struct{}

func (noopSender) Send(context.Context, mailer.Config, mailer.Message) error { return nil }

func TestSMTPConfigHandlers(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), noopSender{})
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

	saveBody, _ := json.Marshal(map[string]any{
		"host": "smtp.example.com", "port": 587, "useTls": true,
		"username": "smtp-user", "password": "s3cret",
		"fromAddress": "no-reply@example.com", "fromName": "ArgusOps",
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
