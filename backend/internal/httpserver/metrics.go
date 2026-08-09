package httpserver

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// numDurationBuckets/durationBucketBoundsSeconds define the request-
// duration histogram's buckets -- a fixed, hand-rolled set close to
// Prometheus's own client library defaults, small enough to cover typical
// request latency plus the occasional slow one (e.g. a synchronous
// validation step) without adding client_golang as a dependency just for
// this (see this project's general lean-dependency stance).
const numDurationBuckets = 9

var durationBucketBoundsSeconds = [numDurationBuckets]float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// MetricsCollector tracks HTTP statistics for Prometheus scraping.
type MetricsCollector struct {
	httpRequestsTotal atomic.Uint64
	http2xxTotal      atomic.Uint64
	http4xxTotal      atomic.Uint64
	http5xxTotal      atomic.Uint64
	activeSSE         atomic.Int64

	durationBucketCounts [numDurationBuckets]atomic.Uint64
	durationCount        atomic.Uint64
	durationSumMicros    atomic.Uint64

	// pool is optional (see SetPool) -- MetricsHandler simply omits the
	// argusops_db_pool_* gauges when it's never registered.
	pool *pgxpool.Pool
}

var globalMetrics = &MetricsCollector{}

// GetMetrics returns the global metrics collector instance.
func GetMetrics() *MetricsCollector {
	return globalMetrics
}

func (m *MetricsCollector) RecordRequest(status int, duration time.Duration) {
	m.httpRequestsTotal.Add(1)
	switch {
	case status >= 200 && status < 300:
		m.http2xxTotal.Add(1)
	case status >= 400 && status < 500:
		m.http4xxTotal.Add(1)
	case status >= 500:
		m.http5xxTotal.Add(1)
	}

	m.durationCount.Add(1)
	m.durationSumMicros.Add(uint64(duration.Microseconds())) //nolint:gosec // duration is never negative
	seconds := duration.Seconds()
	// Prometheus histogram buckets are cumulative (le="X" counts every
	// observation <= X, not just the ones that specific bin covers) -- bump
	// every bucket boundary this observation falls at or under.
	for i, bound := range durationBucketBoundsSeconds {
		if seconds <= bound {
			m.durationBucketCounts[i].Add(1)
		}
	}
}

func (m *MetricsCollector) IncSSE() {
	m.activeSSE.Add(1)
}

func (m *MetricsCollector) DecSSE() {
	m.activeSSE.Add(-1)
}

// SetPool registers the pool MetricsHandler reports argusops_db_pool_*
// gauges for -- called once at startup (see cmd/api/main.go,
// cmd/worker/main.go). Sizing these correctly matters once running more
// than one replica (see config.Config's DBPoolMaxConns doc comment); seeing
// idle vs. acquired vs. the configured max here is what tells you whether
// the current sizing is actually adequate instead of guessing.
func (m *MetricsCollector) SetPool(pool *pgxpool.Pool) {
	m.pool = pool
}

// MetricsMiddleware wraps handlers to collect status code and duration metrics.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			GetMetrics().RecordRequest(ww.Status(), time.Since(start))
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

	fmt.Fprintf(w, "# HELP argusops_http_request_duration_seconds HTTP request duration in seconds.\n")
	fmt.Fprintf(w, "# TYPE argusops_http_request_duration_seconds histogram\n")
	for i, bound := range durationBucketBoundsSeconds {
		fmt.Fprintf(w, "argusops_http_request_duration_seconds_bucket{le=\"%g\"} %d\n", bound, m.durationBucketCounts[i].Load())
	}
	count := m.durationCount.Load()
	fmt.Fprintf(w, "argusops_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", count)
	fmt.Fprintf(w, "argusops_http_request_duration_seconds_sum %g\n", float64(m.durationSumMicros.Load())/1e6)
	fmt.Fprintf(w, "argusops_http_request_duration_seconds_count %d\n\n", count)

	fmt.Fprintf(w, "# HELP argusops_sse_active_connections Current active SSE stream connections.\n")
	fmt.Fprintf(w, "# TYPE argusops_sse_active_connections gauge\n")
	fmt.Fprintf(w, "argusops_sse_active_connections %d\n", m.activeSSE.Load())

	if m.pool != nil {
		stat := m.pool.Stat()
		fmt.Fprintf(w, "\n# HELP argusops_db_pool_total_conns Current total connections (idle + in use) in the pool.\n")
		fmt.Fprintf(w, "# TYPE argusops_db_pool_total_conns gauge\n")
		fmt.Fprintf(w, "argusops_db_pool_total_conns %d\n\n", stat.TotalConns())

		fmt.Fprintf(w, "# HELP argusops_db_pool_idle_conns Current idle connections in the pool.\n")
		fmt.Fprintf(w, "# TYPE argusops_db_pool_idle_conns gauge\n")
		fmt.Fprintf(w, "argusops_db_pool_idle_conns %d\n\n", stat.IdleConns())

		fmt.Fprintf(w, "# HELP argusops_db_pool_acquired_conns Current in-use (acquired) connections in the pool.\n")
		fmt.Fprintf(w, "# TYPE argusops_db_pool_acquired_conns gauge\n")
		fmt.Fprintf(w, "argusops_db_pool_acquired_conns %d\n\n", stat.AcquiredConns())

		fmt.Fprintf(w, "# HELP argusops_db_pool_max_conns The pool's configured maximum size.\n")
		fmt.Fprintf(w, "# TYPE argusops_db_pool_max_conns gauge\n")
		fmt.Fprintf(w, "argusops_db_pool_max_conns %d\n", stat.MaxConns())
	}
}
