package httpserver

import (
	"context"
	"errors"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuruops/kuruops/internal/buildinfo"
	"github.com/kuruops/kuruops/internal/domain"
)

func TestMetricsHandler(t *testing.T) {
	m := NewMetricsCollector()
	orig := SetMetricsForTest(m)
	defer SetMetricsForTest(orig)
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
	m := NewMetricsCollector()
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
	m := NewMetricsCollector()
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

	// client_golang prints large values in scientific notation (1.76e+09), which is
	// valid exposition format, so read the value as a float.
	var got float64
	line := body[strings.Index(body, `kuruops_worker_last_sweep_success_timestamp{sweep_job="sweep_escalations"}`):]
	if _, err := fmt.Sscanf(line, `kuruops_worker_last_sweep_success_timestamp{sweep_job="sweep_escalations"} %g`, &got); err != nil {
		t.Fatalf("parse gauge value: %v", err)
	}
	if got < float64(before) || got > float64(after) {
		t.Fatalf("expected the recorded timestamp to fall within [%d, %d], got %v", before, after, got)
	}
}

func TestMetricsHandler_OmitsPoolGaugesWhenPoolNotSet(t *testing.T) {
	m := NewMetricsCollector()
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
	m := NewMetricsCollector()
	m.IncSSE()
	m.IncSSE()
	m.DecSSE()
	if got := testutil.ToFloat64(m.activeSSE); got != 1 {
		t.Fatalf("expected activeSSE=1 after 2 inc + 1 dec, got %v", got)
	}
}

func TestMetricsCollector_RecordRequest_BucketsAreCumulative(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordRequest(200, 30*time.Millisecond) // falls in the 0.05s bucket and every larger one

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`kuruops_http_request_duration_seconds_bucket{le="0.01"} 0`, // stays empty
		`kuruops_http_request_duration_seconds_bucket{le="0.05"} 1`,
		`kuruops_http_request_duration_seconds_bucket{le="10"} 1`, // cumulative
		`kuruops_http_request_duration_seconds_bucket{le="+Inf"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output is missing %q", want)
		}
	}
}

func TestMetricsMiddleware_RecordsStatusCode(t *testing.T) {
	m := NewMetricsCollector()
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
	if got := testutil.ToFloat64(m.requests); got != 1 {
		t.Fatalf("expected requests=1, got %v", got)
	}
	if got := testutil.ToFloat64(m.requests2xx); got != 1 {
		t.Fatalf("expected requests2xx=1, got %v", got)
	}
	if got := testutil.CollectAndCount(m.duration); got != 1 {
		t.Fatalf("expected the duration histogram to have one series, got %d", got)
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
		m := NewMetricsCollector()
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
		if body := scrape(NewMetricsCollector()); strings.Contains(body, "kuruops_ai_analysis") {
			t.Errorf("api/worker must not report queue gauges they never registered:\n%s", body)
		}
	})

	t.Run("a failing read is skipped, not fatal to the scrape", func(t *testing.T) {
		m := NewMetricsCollector()
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
