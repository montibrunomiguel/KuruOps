package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsHandler(t *testing.T) {
	m := GetMetrics()
	m.RecordRequest(200, 15*time.Millisecond)
	m.RecordRequest(404, 5*time.Millisecond)
	m.RecordRequest(500, 3*time.Second)
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
	if !strings.Contains(body, "argusops_http_request_duration_seconds_bucket") {
		t.Fatal("expected body to contain argusops_http_request_duration_seconds_bucket")
	}
	if !strings.Contains(body, `argusops_http_request_duration_seconds_bucket{le="+Inf"} 3`) {
		t.Fatal("expected +Inf bucket to count all 3 observations")
	}
	if !strings.Contains(body, "argusops_http_request_duration_seconds_count 3") {
		t.Fatal("expected duration count of 3")
	}
}

func TestMetricsHandler_OmitsPoolGaugesWhenPoolNotSet(t *testing.T) {
	m := &MetricsCollector{}
	orig := globalMetrics
	globalMetrics = m
	defer func() { globalMetrics = orig }()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	MetricsHandler(rr, req)

	if strings.Contains(rr.Body.String(), "argusops_db_pool_total_conns") {
		t.Fatal("expected no db_pool gauges when SetPool was never called")
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

func TestMetricsCollector_RecordRequest_BucketsAreCumulative(t *testing.T) {
	m := &MetricsCollector{}
	m.RecordRequest(200, 30*time.Millisecond) // falls in the 0.05s bucket and every larger one

	if got := m.durationBucketCounts[0].Load(); got != 0 { // le=0.01
		t.Fatalf("expected le=0.01 bucket to stay 0, got %d", got)
	}
	if got := m.durationBucketCounts[1].Load(); got != 1 { // le=0.05
		t.Fatalf("expected le=0.05 bucket to be 1, got %d", got)
	}
	if got := m.durationBucketCounts[numDurationBuckets-1].Load(); got != 1 { // le=10, cumulative
		t.Fatalf("expected le=10 (largest) bucket to be 1, got %d", got)
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
	if got := m.durationCount.Load(); got != 1 {
		t.Fatalf("expected durationCount=1, got %d", got)
	}
}
