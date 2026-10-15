package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuruops/kuruops/internal/buildinfo"
	"github.com/kuruops/kuruops/internal/domain"
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
	if !strings.Contains(body, "kuruops_http_requests_total") {
		t.Fatal("expected body to contain kuruops_http_requests_total")
	}
	if !strings.Contains(body, "kuruops_sse_active_connections") {
		t.Fatal("expected body to contain kuruops_sse_active_connections")
	}
	if !strings.Contains(body, "kuruops_http_request_duration_seconds_bucket") {
		t.Fatal("expected body to contain kuruops_http_request_duration_seconds_bucket")
	}
	if !strings.Contains(body, `kuruops_http_request_duration_seconds_bucket{le="+Inf"} 3`) {
		t.Fatal("expected +Inf bucket to count all 3 observations")
	}
	if !strings.Contains(body, "kuruops_http_request_duration_seconds_count 3") {
		t.Fatal("expected duration count of 3")
	}
}

// TestMetricsHandler_OmitsSweepGaugeWhenNoJobHasRunYet confirms a fresh
// process (no sweep tick has completed yet) doesn't emit the
// kuruops_worker_last_sweep_success_timestamp series at all -- a metric
// that's simply absent is normal in Prometheus, unlike one that's present
// with a misleading zero value (which would read as "last successful run
// was at the Unix epoch", immediately tripping a staleness alert).
func TestMetricsHandler_OmitsSweepGaugeWhenNoJobHasRunYet(t *testing.T) {
	m := &MetricsCollector{}
	orig := globalMetrics
	globalMetrics = m
	defer func() { globalMetrics = orig }()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	MetricsHandler(rr, req)

	if strings.Contains(rr.Body.String(), "kuruops_worker_last_sweep_success_timestamp") {
		t.Fatal("expected no sweep-success gauge before any job has recorded a success")
	}
}

func TestMetricsCollector_RecordSweepSuccess(t *testing.T) {
	m := &MetricsCollector{}
	orig := globalMetrics
	globalMetrics = m
	defer func() { globalMetrics = orig }()

	before := time.Now().Unix()
	m.RecordSweepSuccess("sweep_escalations")
	after := time.Now().Unix()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	MetricsHandler(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, `kuruops_worker_last_sweep_success_timestamp{sweep_job="sweep_escalations"}`) {
		t.Fatalf("expected the sweep-success gauge labeled by job, got body: %s", body)
	}

	var got int64
	line := body[strings.Index(body, `kuruops_worker_last_sweep_success_timestamp{sweep_job="sweep_escalations"}`):]
	if _, err := fmt.Sscanf(line, `kuruops_worker_last_sweep_success_timestamp{sweep_job="sweep_escalations"} %d`, &got); err != nil {
		t.Fatalf("parse gauge value: %v", err)
	}
	if got < before || got > after {
		t.Fatalf("expected the recorded timestamp to fall within [%d, %d], got %d", before, after, got)
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

	if strings.Contains(rr.Body.String(), "kuruops_db_pool_total_conns") {
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

func TestMetricsHandler_AIQueueGauges(t *testing.T) {
	scrape := func(m *MetricsCollector) string {
		prev := SetMetricsForTest(m)
		defer SetMetricsForTest(prev)
		rr := httptest.NewRecorder()
		MetricsHandler(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return rr.Body.String()
	}

	t.Run("reports depth by status and the age of the oldest due job", func(t *testing.T) {
		m := &MetricsCollector{}
		m.SetAIQueueStats(func(context.Context) (domain.AIAnalysisJobStats, error) {
			return domain.AIAnalysisJobStats{Pending: 4, Running: 2, Succeeded: 90, Failed: 3, Skipped: 1, OldestPendingAge: 95 * time.Second}, nil
		})
		body := scrape(m)
		for _, want := range []string{
			`kuruops_ai_analysis_jobs{status="pending"} 4`,
			`kuruops_ai_analysis_jobs{status="running"} 2`,
			`kuruops_ai_analysis_jobs{status="succeeded"} 90`,
			`kuruops_ai_analysis_jobs{status="failed"} 3`,
			`kuruops_ai_analysis_jobs{status="skipped"} 1`,
			`kuruops_ai_analysis_oldest_pending_seconds 95`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("metrics output is missing %q", want)
			}
		}
	})

	t.Run("a process that does not work the queue emits nothing about it", func(t *testing.T) {
		if body := scrape(&MetricsCollector{}); strings.Contains(body, "kuruops_ai_analysis") {
			t.Errorf("api/worker must not report queue gauges they never registered:\n%s", body)
		}
	})

	t.Run("a failing read is skipped, not fatal to the scrape", func(t *testing.T) {
		m := &MetricsCollector{}
		m.SetAIQueueStats(func(context.Context) (domain.AIAnalysisJobStats, error) {
			return domain.AIAnalysisJobStats{}, errors.New("database down")
		})
		body := scrape(m)
		if strings.Contains(body, "kuruops_ai_analysis") {
			t.Error("no series should be emitted when the stats cannot be read -- absent is how Prometheus says 'no data'")
		}
		if !strings.Contains(body, "kuruops_http_requests_total") {
			t.Error("the rest of the scrape must still be served")
		}
	})
}

// TestMetricsHandler_ExposesBuildInfo pins the series the partial-deploy alert
// (deploy/k8s/09-monitoring.yaml, KuruOpsBuildSkew) counts distinct values of.
func TestMetricsHandler_ExposesBuildInfo(t *testing.T) {
	prev := buildinfo.Build
	buildinfo.Build = "abc123def456"
	t.Cleanup(func() { buildinfo.Build = prev })

	rr := httptest.NewRecorder()
	MetricsHandler(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if want := `kuruops_build_info{build="abc123def456"} 1`; !strings.Contains(rr.Body.String(), want) {
		t.Fatalf("metrics output missing %q:\n%s", want, rr.Body.String())
	}
}
