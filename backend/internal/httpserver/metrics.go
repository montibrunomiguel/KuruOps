package httpserver

import (
	"fmt"
	"net/http"
	"sync"
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
	// kuruops_db_pool_* gauges when it's never registered.
	pool *pgxpool.Pool

	// lastSweepSuccessUnix tracks, per cmd/worker sweep job name (see
	// runLocked's job strings), the unix time it last completed a tick
	// without panicking -- a zero-value sync.Map needs no constructor,
	// matching every other field here being safe to use straight off
	// &MetricsCollector{} (see this type's own test fixtures). This is the
	// first labeled metric in this file, by necessity: an unlabeled single
	// gauge couldn't distinguish "escalations sweep is stuck" from "SLA
	// sweep is stuck". The label is named sweep_job, not job -- Prometheus
	// auto-injects its own "job" label onto every scraped series from the
	// scrape config's job_name (see deploy/k8s/09-monitoring.yaml), so a
	// custom label also called "job" would silently collide with it.
	lastSweepSuccessUnix sync.Map // map[string]int64
}

// RecordSweepSuccess stamps job's last-success time to now -- called by
// cmd/worker's runLocked immediately after a sweep tick's fn returns
// without panicking. Only the replica that actually won the tick's
// Postgres advisory lock calls this (see db.Pool.WithAdvisoryLock), so in
// a single-worker-replica deployment (the current default -- see
// deploy/k8s/05-ingest.yaml's sibling comment on why worker stays at 1) the
// exposed gauge is exactly "when did this job last actually run". Scaling
// worker to more than one replica would need an alert rule that takes the
// max across replicas' series, not a per-pod threshold, since only whichever
// replica wins a given tick updates its own local value.
func (m *MetricsCollector) RecordSweepSuccess(job string) {
	m.lastSweepSuccessUnix.Store(job, time.Now().Unix())
}

var globalMetrics = &MetricsCollector{}

// GetMetrics returns the global metrics collector instance.
func GetMetrics() *MetricsCollector {
	return globalMetrics
}

// SetMetricsForTest overrides the global metrics collector and returns the
// previous one -- lets a test in another package (e.g. cmd/worker, which
// can't reach the unexported globalMetrics var this package's own tests
// swap directly) isolate its assertions from whatever the shared global
// collector has already accumulated. Callers should restore the returned
// value when done, same pattern as this package's own tests.
func SetMetricsForTest(m *MetricsCollector) *MetricsCollector {
	old := globalMetrics
	globalMetrics = m
	return old
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

// SetPool registers the pool MetricsHandler reports kuruops_db_pool_*
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

	fmt.Fprintf(w, "# HELP kuruops_http_requests_total Total number of HTTP requests processed.\n")
	fmt.Fprintf(w, "# TYPE kuruops_http_requests_total counter\n")
	fmt.Fprintf(w, "kuruops_http_requests_total %d\n\n", m.httpRequestsTotal.Load())

	fmt.Fprintf(w, "# HELP kuruops_http_requests_2xx_total Total number of 2xx HTTP responses.\n")
	fmt.Fprintf(w, "# TYPE kuruops_http_requests_2xx_total counter\n")
	fmt.Fprintf(w, "kuruops_http_requests_2xx_total %d\n\n", m.http2xxTotal.Load())

	fmt.Fprintf(w, "# HELP kuruops_http_requests_4xx_total Total number of 4xx HTTP responses.\n")
	fmt.Fprintf(w, "# TYPE kuruops_http_requests_4xx_total counter\n")
	fmt.Fprintf(w, "kuruops_http_requests_4xx_total %d\n\n", m.http4xxTotal.Load())

	fmt.Fprintf(w, "# HELP kuruops_http_requests_5xx_total Total number of 5xx HTTP responses.\n")
	fmt.Fprintf(w, "# TYPE kuruops_http_requests_5xx_total counter\n")
	fmt.Fprintf(w, "kuruops_http_requests_5xx_total %d\n\n", m.http5xxTotal.Load())

	fmt.Fprintf(w, "# HELP kuruops_http_request_duration_seconds HTTP request duration in seconds.\n")
	fmt.Fprintf(w, "# TYPE kuruops_http_request_duration_seconds histogram\n")
	for i, bound := range durationBucketBoundsSeconds {
		fmt.Fprintf(w, "kuruops_http_request_duration_seconds_bucket{le=\"%g\"} %d\n", bound, m.durationBucketCounts[i].Load())
	}
	count := m.durationCount.Load()
	fmt.Fprintf(w, "kuruops_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", count)
	fmt.Fprintf(w, "kuruops_http_request_duration_seconds_sum %g\n", float64(m.durationSumMicros.Load())/1e6)
	fmt.Fprintf(w, "kuruops_http_request_duration_seconds_count %d\n\n", count)

	fmt.Fprintf(w, "# HELP kuruops_sse_active_connections Current active SSE stream connections.\n")
	fmt.Fprintf(w, "# TYPE kuruops_sse_active_connections gauge\n")
	fmt.Fprintf(w, "kuruops_sse_active_connections %d\n", m.activeSSE.Load())

	// Absent entirely on a fresh process until each job's first tick
	// completes -- Prometheus tolerates a gauge series simply not existing
	// yet, no need to pre-seed zeros for jobs that haven't run.
	var sweepLines []string
	m.lastSweepSuccessUnix.Range(func(k, v any) bool {
		sweepLines = append(sweepLines, fmt.Sprintf("kuruops_worker_last_sweep_success_timestamp{sweep_job=%q} %d\n", k, v))
		return true
	})
	if len(sweepLines) > 0 {
		fmt.Fprintf(w, "\n# HELP kuruops_worker_last_sweep_success_timestamp Unix time each named cmd/worker sweep job last completed a tick without panicking.\n")
		fmt.Fprintf(w, "# TYPE kuruops_worker_last_sweep_success_timestamp gauge\n")
		for _, line := range sweepLines {
			fmt.Fprint(w, line)
		}
	}

	if m.pool != nil {
		stat := m.pool.Stat()
		fmt.Fprintf(w, "\n# HELP kuruops_db_pool_total_conns Current total connections (idle + in use) in the pool.\n")
		fmt.Fprintf(w, "# TYPE kuruops_db_pool_total_conns gauge\n")
		fmt.Fprintf(w, "kuruops_db_pool_total_conns %d\n\n", stat.TotalConns())

		fmt.Fprintf(w, "# HELP kuruops_db_pool_idle_conns Current idle connections in the pool.\n")
		fmt.Fprintf(w, "# TYPE kuruops_db_pool_idle_conns gauge\n")
		fmt.Fprintf(w, "kuruops_db_pool_idle_conns %d\n\n", stat.IdleConns())

		fmt.Fprintf(w, "# HELP kuruops_db_pool_acquired_conns Current in-use (acquired) connections in the pool.\n")
		fmt.Fprintf(w, "# TYPE kuruops_db_pool_acquired_conns gauge\n")
		fmt.Fprintf(w, "kuruops_db_pool_acquired_conns %d\n\n", stat.AcquiredConns())

		fmt.Fprintf(w, "# HELP kuruops_db_pool_max_conns The pool's configured maximum size.\n")
		fmt.Fprintf(w, "# TYPE kuruops_db_pool_max_conns gauge\n")
		fmt.Fprintf(w, "kuruops_db_pool_max_conns %d\n", stat.MaxConns())
	}
}
