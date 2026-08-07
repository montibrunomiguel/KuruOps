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
