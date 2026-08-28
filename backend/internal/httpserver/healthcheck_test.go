package httpserver

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
)

func TestHealthCheck_UnreachableDatabaseReturns503(t *testing.T) {
	// A pool pointed at a port nothing listens on fails to ping quickly
	// without needing a real Postgres instance in this unit test -- the
	// happy path (a real, reachable pool returning 200) is covered by this
	// front's live verification against the real docker-compose stack.
	cfg, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Close()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	HealthCheck(pool)(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

// TestLivez_AlwaysReturns200 confirms /livez has no downstream dependency
// (unlike HealthCheck) -- a nil pool would panic HealthCheck, but Livez
// doesn't take one at all, so there's nothing to fail regardless of
// database state.
func TestLivez_AlwaysReturns200(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rec := httptest.NewRecorder()
	Livez(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestWrapWithObservability_AssignsRequestIDAndRecordsMetrics(t *testing.T) {
	m := &MetricsCollector{}
	orig := globalMetrics
	globalMetrics = m
	defer func() { globalMetrics = orig }()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	base := http.NewServeMux()
	base.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		middleware.LoggerFromContext(r.Context(), logger).Info("handled")
		w.WriteHeader(http.StatusOK)
	})

	handler := WrapWithObservability(base, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := m.httpRequestsTotal.Load(); got != 1 {
		t.Fatalf("expected the request to be recorded in metrics, got %d", got)
	}

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("unmarshal log line: %v, raw: %s", err, buf.String())
	}
	if _, ok := entry["request_id"]; !ok {
		t.Fatalf("expected the handler's logger (from context) to carry request_id, got %v", entry)
	}
}

// TestWrapWithObservability_RecoversFromPanic is the regression test for the
// cmd/ingest /hooks crash scenario: before chimw.Recoverer was added to this
// chain, a handler panic (e.g. a malformed webhook payload panicking a
// normalizer) propagated all the way out of ServeHTTP, which would crash the
// whole ingest process for every tenant instead of just failing the one
// request. A handler calling ServeHTTP directly (as this test does, and as
// httptest.Server would) with no recover() of its own would otherwise see
// the panic re-thrown here and fail the test process itself, proving
// Recoverer is actually in the chain rather than just present in source.
func TestWrapWithObservability_RecoversFromPanic(t *testing.T) {
	// MetricsMiddleware's deferred RecordRequest call still fires while the
	// panic unwinds through it (defers run during unwinding, even though
	// Recoverer -- further out in the chain -- is what ultimately stops the
	// panic) -- swap out the global collector so that doesn't pollute
	// TestMetricsHandler's exact-count assertions elsewhere in this package,
	// same isolation TestWrapWithObservability_AssignsRequestIDAndRecordsMetrics
	// above already uses.
	orig := globalMetrics
	globalMetrics = &MetricsCollector{}
	defer func() { globalMetrics = orig }()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	base := http.NewServeMux()
	base.HandleFunc("/hooks", func(w http.ResponseWriter, r *http.Request) {
		panic("malformed payload")
	})

	handler := WrapWithObservability(base, logger, nil)

	req := httptest.NewRequest(http.MethodPost, "/hooks", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected the panic to be recovered into a 500, got %d", rec.Code)
	}
}
