package httpserver

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/kuruops/kuruops/internal/buildinfo"
	"github.com/kuruops/kuruops/internal/domain"
)

// durationBucketBoundsSeconds are the request-duration histogram's buckets: close
// to Prometheus's own defaults, wide enough to cover typical latency plus the
// occasional slow request (e.g. a synchronous validation step). Kept identical to
// what the hand-written exposition used, so existing dashboards and the
// KuruOpsHighLatencyP99 rule see the same series.
var durationBucketBoundsSeconds = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// MetricsCollector holds this process's Prometheus metrics. It used to render the
// exposition format by hand (counters in atomics, a histogram in a bucket array,
// HELP/TYPE lines in Fprintf); it is now a thin layer over client_golang, which
// gets the format, escaping, content negotiation and the Go-runtime/process
// collectors right without this package maintaining them. Every metric name and
// label from the hand-written version is preserved -- alert rules depend on them.
//
// Each MetricsCollector has its own registry, so a test can build one and not see
// (or disturb) what another test or the process-wide instance has recorded.
type MetricsCollector struct {
	registry *prometheus.Registry

	requests    prometheus.Counter
	requests2xx prometheus.Counter
	requests4xx prometheus.Counter
	requests5xx prometheus.Counter
	duration    prometheus.Histogram
	activeSSE   prometheus.Gauge

	// lastSweepSuccess is labelled sweep_job, not job: Prometheus injects its own
	// "job" label onto every scraped series from the scrape config's job_name (see
	// deploy/k8s/09-monitoring.yaml), so a custom label also called "job" would
	// silently collide with it.
	lastSweepSuccess *prometheus.GaugeVec

	// pool and aiQueue are read at scrape time by their collectors, and are optional:
	// until SetPool / SetAIQueueStats is called the series are simply absent.
	pool    atomic.Pointer[pgxpool.Pool]
	aiQueue atomic.Pointer[aiQueueSource]
}

type aiQueueSource struct {
	stats func(ctx context.Context) (domain.AIAnalysisJobStats, error)
}

// NewMetricsCollector builds a collector with a fresh registry holding the HTTP,
// SSE, sweep, build-info, DB-pool and AI-queue metrics plus the standard Go
// runtime and process collectors.
func NewMetricsCollector() *MetricsCollector {
	m := &MetricsCollector{registry: prometheus.NewRegistry()}

	m.requests = prometheus.NewCounter(prometheus.CounterOpts{Name: "kuruops_http_requests_total", Help: "Total number of HTTP requests processed."})
	m.requests2xx = prometheus.NewCounter(prometheus.CounterOpts{Name: "kuruops_http_requests_2xx_total", Help: "Total number of 2xx HTTP responses."})
	m.requests4xx = prometheus.NewCounter(prometheus.CounterOpts{Name: "kuruops_http_requests_4xx_total", Help: "Total number of 4xx HTTP responses."})
	m.requests5xx = prometheus.NewCounter(prometheus.CounterOpts{Name: "kuruops_http_requests_5xx_total", Help: "Total number of 5xx HTTP responses."})
	m.duration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "kuruops_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: durationBucketBoundsSeconds,
	})
	m.activeSSE = prometheus.NewGauge(prometheus.GaugeOpts{Name: "kuruops_sse_active_connections", Help: "Current active SSE stream connections."})
	m.lastSweepSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kuruops_worker_last_sweep_success_timestamp",
		Help: "Unix time each named cmd/worker sweep job last completed a tick without panicking.",
	}, []string{"sweep_job"})

	m.registry.MustRegister(
		m.requests, m.requests2xx, m.requests4xx, m.requests5xx, m.duration,
		m.activeSSE, m.lastSweepSuccess, buildInfoCollector{},
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		&poolCollector{m: m},
		&aiQueueCollector{m: m},
	)
	return m
}

// globalMetrics is created in init, not as a package-level initializer: the
// collectors above describe themselves with package-level Descs, and Go does not
// see that dependency through the interface call, so an initializer could run
// before they exist.
var globalMetrics *MetricsCollector

func init() { globalMetrics = NewMetricsCollector() }

// GetMetrics returns the global metrics collector instance.
func GetMetrics() *MetricsCollector {
	return globalMetrics
}

// SetMetricsForTest overrides the global metrics collector and returns the
// previous one -- lets a test in another package (e.g. cmd/worker, which can't
// reach the unexported globalMetrics var this package's own tests swap directly)
// isolate its assertions from whatever the shared global collector has already
// accumulated. Callers should restore the returned value when done.
func SetMetricsForTest(m *MetricsCollector) *MetricsCollector {
	old := globalMetrics
	globalMetrics = m
	return old
}

// RecordSweepSuccess stamps job's last-success time to now -- called by
// cmd/worker's runLocked immediately after a sweep tick's fn returns without
// panicking. Only the replica that actually won the tick's Postgres advisory lock
// calls this (see db.Pool.WithAdvisoryLock), so with several worker replicas only
// the one that ran the tick updates its own local value: the alert rule has to
// take the max across replicas' series, not a per-pod threshold.
func (m *MetricsCollector) RecordSweepSuccess(job string) {
	// Whole seconds, as the hand-written gauge was, not SetToCurrentTime's fractions.
	m.lastSweepSuccess.WithLabelValues(job).Set(float64(time.Now().Unix()))
}

// RecordRequest counts one finished request and observes its duration.
func (m *MetricsCollector) RecordRequest(status int, duration time.Duration) {
	m.requests.Inc()
	switch {
	case status >= 200 && status < 300:
		m.requests2xx.Inc()
	case status >= 400 && status < 500:
		m.requests4xx.Inc()
	case status >= 500:
		m.requests5xx.Inc()
	}
	m.duration.Observe(duration.Seconds())
}

func (m *MetricsCollector) IncSSE() { m.activeSSE.Inc() }

func (m *MetricsCollector) DecSSE() { m.activeSSE.Dec() }

// SetPool registers the pool kuruops_db_pool_* gauges are reported for -- called
// once at startup. Sizing these correctly matters once running more than one
// replica (see config.Config's DBPoolMaxConns doc comment); seeing idle vs.
// acquired vs. the configured max here is what tells you whether the current
// sizing is actually adequate instead of guessing.
func (m *MetricsCollector) SetPool(pool *pgxpool.Pool) {
	m.pool.Store(pool)
}

// SetAIQueueStats registers where the auto-analysis queue's health is read from,
// reported as kuruops_ai_analysis_jobs{status} and
// kuruops_ai_analysis_oldest_pending_seconds. Called once at startup by the
// process that consumes the queue. It is read at scrape time, so the numbers are
// exact rather than counters a restart would reset.
func (m *MetricsCollector) SetAIQueueStats(fn func(ctx context.Context) (domain.AIAnalysisJobStats, error)) {
	m.aiQueue.Store(&aiQueueSource{stats: fn})
}

// Handler serves this collector's metrics in the Prometheus exposition format.
func (m *MetricsCollector) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
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
	GetMetrics().Handler().ServeHTTP(w, r)
}

// poolCollector reports the database pool's gauges, read when scraped.
type poolCollector struct{ m *MetricsCollector }

var (
	poolTotalDesc    = prometheus.NewDesc("kuruops_db_pool_total_conns", "Current total connections (idle + in use) in the pool.", nil, nil)
	poolIdleDesc     = prometheus.NewDesc("kuruops_db_pool_idle_conns", "Current idle connections in the pool.", nil, nil)
	poolAcquiredDesc = prometheus.NewDesc("kuruops_db_pool_acquired_conns", "Current in-use (acquired) connections in the pool.", nil, nil)
	poolMaxDesc      = prometheus.NewDesc("kuruops_db_pool_max_conns", "The pool's configured maximum size.", nil, nil)
)

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- poolTotalDesc
	ch <- poolIdleDesc
	ch <- poolAcquiredDesc
	ch <- poolMaxDesc
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	pool := c.m.pool.Load()
	if pool == nil {
		return
	}
	stat := pool.Stat()
	ch <- prometheus.MustNewConstMetric(poolTotalDesc, prometheus.GaugeValue, float64(stat.TotalConns()))
	ch <- prometheus.MustNewConstMetric(poolIdleDesc, prometheus.GaugeValue, float64(stat.IdleConns()))
	ch <- prometheus.MustNewConstMetric(poolAcquiredDesc, prometheus.GaugeValue, float64(stat.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(poolMaxDesc, prometheus.GaugeValue, float64(stat.MaxConns()))
}

// aiQueueCollector reports the auto-analysis queue gauges. A failing read is
// skipped, not fatal: a metrics scrape must not fail (or hang) because the
// database had a moment, and an absent series is how Prometheus already
// represents "no data".
type aiQueueCollector struct{ m *MetricsCollector }

var (
	aiJobsDesc          = prometheus.NewDesc("kuruops_ai_analysis_jobs", "Automatic-analysis jobs by status.", []string{"status"}, nil)
	aiOldestPendingDesc = prometheus.NewDesc("kuruops_ai_analysis_oldest_pending_seconds", "How long the oldest due automatic-analysis job has been waiting; 0 when none is due. A queue that is not draining shows up here first.", nil, nil)
)

func (c *aiQueueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- aiJobsDesc
	ch <- aiOldestPendingDesc
}

func (c *aiQueueCollector) Collect(ch chan<- prometheus.Metric) {
	src := c.m.aiQueue.Load()
	if src == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := src.stats(ctx)
	if err != nil {
		return
	}
	for _, s := range []struct {
		status string
		n      int
	}{{"pending", st.Pending}, {"running", st.Running}, {"succeeded", st.Succeeded}, {"failed", st.Failed}, {"skipped", st.Skipped}} {
		ch <- prometheus.MustNewConstMetric(aiJobsDesc, prometheus.GaugeValue, float64(s.n), s.status)
	}
	ch <- prometheus.MustNewConstMetric(aiOldestPendingDesc, prometheus.GaugeValue, st.OldestPendingAge.Seconds())
}

// buildInfoCollector exports kuruops_build_info{build} = 1, reading the build id when
// scraped. The partial-deploy alert (KuruOpsBuildSkew) counts distinct values of the
// label across api, ingest and worker.
type buildInfoCollector struct{}

var buildInfoDesc = prometheus.NewDesc("kuruops_build_info", "Always 1; the build label identifies the sources this binary was compiled from. More than one distinct value across api/ingest/worker means a partial deploy.", []string{"build"}, nil)

func (buildInfoCollector) Describe(ch chan<- *prometheus.Desc) { ch <- buildInfoDesc }

func (buildInfoCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(buildInfoDesc, prometheus.GaugeValue, 1, buildinfo.Build)
}
