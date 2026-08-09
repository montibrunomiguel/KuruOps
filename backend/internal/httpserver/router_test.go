package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/argusops/argusops/internal/httpserver/handlers"
)

// nilOptions builds Options with every handler set to its zero-value
// pointer (nil) -- safe here because NewRouter only ever registers each
// handler's method *value* with chi (chi.Route(pattern, h.Routes) etc.),
// it never calls into a handler while building the router. This test is
// about route wiring/middleware ordering, not handler behavior (that's
// each handlers_test.go file's job).
func nilOptions() Options {
	return Options{
		AlertHandlers:             &handlers.AlertHandlers{},
		IncidentHandlers:          &handlers.IncidentHandlers{},
		PlaybookHandlers:          &handlers.PlaybookHandlers{},
		DashboardHandlers:         &handlers.DashboardHandlers{},
		WebhookHandlers:           &handlers.WebhookHandlers{},
		LLMProviderHandlers:       &handlers.LLMProviderHandlers{},
		MCPServerHandlers:         &handlers.MCPServerHandlers{},
		UserHandlers:              &handlers.UserHandlers{},
		AuthHandlers:              &handlers.AuthHandlers{},
		AccountHandlers:           &handlers.AccountHandlers{},
		IdentityConfigHandlers:    &handlers.IdentityConfigHandlers{},
		TagHandlers:               &handlers.TagHandlers{},
		UploadHandlers:            &handlers.UploadHandlers{},
		StorageConfigHandlers:     &handlers.StorageConfigHandlers{},
		SMTPConfigHandlers:        &handlers.SMTPConfigHandlers{},
		OnCallShiftHandlers:       &handlers.OnCallShiftHandlers{},
		IncidentSLAHandlers:       &handlers.IncidentSLAHandlers{},
		EscalationPolicyHandlers:  &handlers.EscalationPolicyHandlers{},
		AuditExportHandlers:       &handlers.AuditExportHandlers{},
		DatabaseMigrationHandlers: &handlers.DatabaseMigrationHandlers{},
		EventsHandlers:            &handlers.EventsHandlers{},
		// A pass-through, not nil -- NewRouter wires this into the /auth
		// route chain unconditionally (see router.go), so a nil func value
		// here would panic building the router, not just when /auth is hit.
		LoginRateLimiter: func(next http.Handler) http.Handler { return next },
		AuthMiddleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			})
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		HealthCheck: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		},
	}
}

func TestNewRouter_Healthz(t *testing.T) {
	r := NewRouter(nilOptions())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", rec.Body.String())
	}
}

func TestNewRouter_Metrics(t *testing.T) {
	r := NewRouter(nilOptions())

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestNewRouter_APIRoutesGoThroughAuthMiddleware confirms /api/v1/** is
// actually wired behind opts.AuthMiddleware, not just healthz/metrics --
// the fake middleware above always returns 401 without calling next, so any
// response other than 401 here would mean a route escaped the auth gate.
func TestNewRouter_APIRoutesGoThroughAuthMiddleware(t *testing.T) {
	r := NewRouter(nilOptions())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tags", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from AuthMiddleware, got %d", rec.Code)
	}
}
