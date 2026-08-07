package httpserver

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/go-chi/chi/v5/middleware"
)

// MetricsCollector tracks HTTP statistics for Prometheus scraping.
type MetricsCollector struct {
	httpRequestsTotal atomic.Uint64
	http2xxTotal      atomic.Uint64
	http4xxTotal      atomic.Uint64
	http5xxTotal      atomic.Uint64
	activeSSE         atomic.Int64
}

var globalMetrics = &MetricsCollector{}

// GetMetrics returns the global metrics collector instance.
func GetMetrics() *MetricsCollector {
	return globalMetrics
}

func (m *MetricsCollector) RecordRequest(status int) {
	m.httpRequestsTotal.Add(1)
	switch {
	case status >= 200 && status < 300:
		m.http2xxTotal.Add(1)
	case status >= 400 && status < 500:
		m.http4xxTotal.Add(1)
	case status >= 500:
		m.http5xxTotal.Add(1)
	}
}

func (m *MetricsCollector) IncSSE() {
	m.activeSSE.Add(1)
}

func (m *MetricsCollector) DecSSE() {
	m.activeSSE.Add(-1)
}

// MetricsMiddleware wraps handlers to collect status code metrics.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			GetMetrics().RecordRequest(ww.Status())
		}()
		next.ServeHTTP(ww, r)
	})
}

// MetricsHandler serves Prometheus exposition format metrics at GET /metrics.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	m := GetMetrics()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, "# HELP argusops_http_requests_total Total number of HTTP requests processed.\n")
	fmt.Fprintf(w, "# TYPE argusops_http_requests_total counter\n")
	fmt.Fprintf(w, "argusops_http_requests_total %d\n\n", m.httpRequestsTotal.Load())

	fmt.Fprintf(w, "# HELP argusops_http_requests_2xx_total Total number of 2xx HTTP responses.\n")
	fmt.Fprintf(w, "# TYPE argusops_http_requests_2xx_total counter\n")
	fmt.Fprintf(w, "argusops_http_requests_2xx_total %d\n\n", m.http2xxTotal.Load())

	fmt.Fprintf(w, "# HELP argusops_http_requests_4xx_total Total number of 4xx HTTP responses.\n")
	fmt.Fprintf(w, "# TYPE argusops_http_requests_4xx_total counter\n")
	fmt.Fprintf(w, "argusops_http_requests_4xx_total %d\n\n", m.http4xxTotal.Load())

	fmt.Fprintf(w, "# HELP argusops_http_requests_5xx_total Total number of 5xx HTTP responses.\n")
	fmt.Fprintf(w, "# TYPE argusops_http_requests_5xx_total counter\n")
	fmt.Fprintf(w, "argusops_http_requests_5xx_total %d\n\n", m.http5xxTotal.Load())

	fmt.Fprintf(w, "# HELP argusops_sse_active_connections Current active SSE stream connections.\n")
	fmt.Fprintf(w, "# TYPE argusops_sse_active_connections gauge\n")
	fmt.Fprintf(w, "argusops_sse_active_connections %d\n", m.activeSSE.Load())
}
