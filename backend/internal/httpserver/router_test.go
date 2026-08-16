package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

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
		AlertHandlers:                &handlers.AlertHandlers{},
		IncidentHandlers:             &handlers.IncidentHandlers{},
		PlaybookHandlers:             &handlers.PlaybookHandlers{},
		DashboardHandlers:            &handlers.DashboardHandlers{},
		WebhookHandlers:              &handlers.WebhookHandlers{},
		FieldMappingTemplateHandlers: &handlers.FieldMappingTemplateHandlers{},
		LLMProviderHandlers:          &handlers.LLMProviderHandlers{},
		MCPServerHandlers:            &handlers.MCPServerHandlers{},
		UserHandlers:                 &handlers.UserHandlers{},
		AuthHandlers:                 &handlers.AuthHandlers{},
		AccountHandlers:              &handlers.AccountHandlers{},
		IdentityConfigHandlers:       &handlers.IdentityConfigHandlers{},
		TagHandlers:                  &handlers.TagHandlers{},
		UploadHandlers:               &handlers.UploadHandlers{},
		StorageConfigHandlers:        &handlers.StorageConfigHandlers{},
		SMTPConfigHandlers:           &handlers.SMTPConfigHandlers{},
		OnCallScheduleHandlers:       &handlers.OnCallScheduleHandlers{},
		IncidentSLAHandlers:          &handlers.IncidentSLAHandlers{},
		EscalationPolicyHandlers:     &handlers.EscalationPolicyHandlers{},
		AuditExportHandlers:          &handlers.AuditExportHandlers{},
		DatabaseMigrationHandlers:    &handlers.DatabaseMigrationHandlers{},
		EventsHandlers:               &handlers.EventsHandlers{},
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

func TestNewRouter_Livez(t *testing.T) {
	r := NewRouter(nilOptions())

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
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

// TestNewRouter_TimeoutAppliesExceptToEventsStream is the regression test
// for the request-timeout wiring: /api/v1/events/stream (SSE, long-lived by
// design) must NOT go through the same chimw.Timeout Group every other
// /api/v1 route does, or every live-update connection would get killed on a
// schedule. Since the real handlers panic on their nil dependencies in this
// package's nilOptions() fixture (see its own doc comment -- these tests
// never actually reach a handler, AuthMiddleware always 401s first), the
// only way to observe the wiring without exercising a handler is to walk
// the built route tree and compare middleware counts: every /api/v1 route
// other than the stream should have exactly one more middleware applied
// (the timeout) than the stream route does.
func TestNewRouter_TimeoutAppliesExceptToEventsStream(t *testing.T) {
	r := NewRouter(nilOptions())

	routes, ok := r.(chi.Routes)
	if !ok {
		t.Fatalf("expected NewRouter's return value to satisfy chi.Routes")
	}

	middlewareCounts := map[string]int{}
	err := chi.Walk(routes, func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		middlewareCounts[method+" "+route] = len(middlewares)
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}

	streamCount, ok := middlewareCounts["GET /api/v1/events/stream"]
	if !ok {
		t.Fatalf("expected /api/v1/events/stream to be registered, got routes: %v", middlewareCounts)
	}
	tagsCount, ok := middlewareCounts["GET /api/v1/tags/"]
	if !ok {
		t.Fatalf("expected /api/v1/tags/ to be registered, got routes: %v", middlewareCounts)
	}

	if tagsCount != streamCount+1 {
		t.Fatalf(
			"expected /api/v1/tags/ to carry exactly one more middleware (the request timeout) than /api/v1/events/stream, got %d vs %d",
			tagsCount, streamCount,
		)
	}

	// The migration route must still carry a RequireAdmin+Timeout pair, same
	// count as any other /settings/* admin route (webhooks, chosen as a
	// route that's still inside the normal HTTPRequestTimeout-bound admin
	// Group) -- it's structurally a SEPARATE api.Group from every other
	// /settings/* route (see router.go), using DatabaseMigrationTimeout
	// instead of HTTPRequestTimeout, but a middleware count alone can't
	// distinguish the two durations, only that both a timeout of *some* kind
	// and RequireAdmin are still applied.
	webhooksCount, ok := middlewareCounts["GET /api/v1/settings/webhooks/"]
	if !ok {
		t.Fatalf("expected /api/v1/settings/webhooks/ to be registered, got routes: %v", middlewareCounts)
	}
	migrateCount, ok := middlewareCounts["POST /api/v1/settings/database-migration/migrate"]
	if !ok {
		t.Fatalf("expected /api/v1/settings/database-migration/migrate to be registered, got routes: %v", middlewareCounts)
	}
	if migrateCount != webhooksCount {
		t.Fatalf(
			"expected /api/v1/settings/database-migration/migrate to carry the same middleware count (RequireAdmin + its own timeout) as /api/v1/settings/webhooks/, got %d vs %d",
			migrateCount, webhooksCount,
		)
	}
}
