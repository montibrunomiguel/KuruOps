package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandler(t *testing.T) {
	m := GetMetrics()
	m.RecordRequest(200)
	m.RecordRequest(404)
	m.RecordRequest(500)
	m.IncSSE()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()

	MetricsHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "argusops_http_requests_total") {
		t.Fatal("expected body to contain argusops_http_requests_total")
	}
	if !strings.Contains(body, "argusops_sse_active_connections") {
		t.Fatal("expected body to contain argusops_sse_active_connections")
	}
}

func TestMetricsCollector_IncDecSSE(t *testing.T) {
	m := &MetricsCollector{}
	m.IncSSE()
	m.IncSSE()
	m.DecSSE()
	if got := m.activeSSE.Load(); got != 1 {
		t.Fatalf("expected activeSSE=1 after 2 inc + 1 dec, got %d", got)
	}
}

func TestMetricsMiddleware_RecordsStatusCode(t *testing.T) {
	m := &MetricsCollector{}
	orig := globalMetrics
	globalMetrics = m
	defer func() { globalMetrics = orig }()

	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if got := m.httpRequestsTotal.Load(); got != 1 {
		t.Fatalf("expected httpRequestsTotal=1, got %d", got)
	}
	if got := m.http2xxTotal.Load(); got != 1 {
		t.Fatalf("expected http2xxTotal=1, got %d", got)
	}
}
