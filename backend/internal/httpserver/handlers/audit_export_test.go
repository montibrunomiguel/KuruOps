package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/handlers"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestAuditExportHandlers_ExportCEF(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	alertRepo := repository.NewAlertRepository()
	svc := service.NewAuditExportService(pool, repository.NewAuditRepository())
	h := handlers.NewAuditExportHandlers(svc)
	r := newRouter(h.Routes)

	t.Run("no history -- 200, empty body, no next-cursor headers", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/cef", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "", rec.Body.String())
		assert.Empty(t, rec.Header().Get("X-Next-Cursor-Created-At"))
	})

	tx := testutil.BeginTx(t, pool, tenantID)
	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))
	require.NoError(t, tx.Commit(t.Context()))

	t.Run("serves a downloadable CEF log with the right headers", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/cef", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment; filename=\"argusops-audit-")
		assert.True(t, strings.HasPrefix(rec.Body.String(), "CEF:0|ArgusOps|ArgusOps|1.0|alert.received|"))
	})

	t.Run("limit=1 returns a next-cursor pair of headers", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/cef?limit=1", nil), tenantID, uuid.New(), nil)
		rec := doRequest(r, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.NotEmpty(t, rec.Header().Get("X-Next-Cursor-Created-At"))
		assert.NotEmpty(t, rec.Header().Get("X-Next-Cursor-Event-Id"))
	})

	t.Run("malformed sinceCreatedAt -- 400", func(t *testing.T) {
		req := withClaims(httptest.NewRequest("GET", "/cef?sinceCreatedAt=not-a-timestamp", nil), tenantID, uuid.New(), nil)
		assert.Equal(t, http.StatusBadRequest, doRequest(r, req).Code)
	})
}

func TestAuditExportHandlers_ExportCEF_MissingTenantContext(t *testing.T) {
	h := handlers.NewAuditExportHandlers(nil)
	r := newRouter(h.Routes)

	req := httptest.NewRequest("GET", "/cef", nil)
	assert.Equal(t, http.StatusUnauthorized, doRequest(r, req).Code)
}
