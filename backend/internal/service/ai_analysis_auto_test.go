package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// autoEnv is a tenant with an alert and a (fake) LLM provider, for exercising
// AIAnalysisService.RunAutoAnalysis -- the synchronous entry point the analysis
// queue calls.
type autoEnv struct {
	pool     *db.Pool
	ai       *service.AIAnalysisService
	analyzed <-chan string
	tenantID uuid.UUID
	actorID  uuid.UUID
	llmSvc   *service.LLMProviderService
	alertSvc *service.AlertService

	mu       sync.Mutex
	requests []map[string]any // every body the fake provider received
	status   int              // what the fake provider answers (200 unless a test changes it)
	// failFirst makes the provider answer 503 to its next N requests, whatever
	// status says, then behave normally -- an outage that ends.
	failFirst int
}

func newAutoEnv(t *testing.T) *autoEnv {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	store := secrets.NewEnvStore()
	e := &autoEnv{pool: pool, tenantID: testutil.NewTenant(t), status: http.StatusOK}
	e.actorID = testutil.NewUser(t, e.tenantID, "analyst", nil)
	e.llmSvc = service.NewLLMProviderService(pool, repository.NewLLMProviderRepository(), store, repository.NewAdminAuditEventRepository())
	e.alertSvc = service.NewAlertService(pool, repository.NewAlertRepository(), service.NewTagService(pool, repository.NewTagRepository(), repository.NewAdminAuditEventRepository()), repository.NewPlaybookRepository())
	e.ai, e.analyzed = newAIAnalysisService(pool, store)
	return e
}

// provider registers the fake LLM as the tenant's default provider.
func (e *autoEnv) provider(t *testing.T, autoAnalyze bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.mu.Lock()
		e.requests = append(e.requests, body)
		status := e.status
		if e.failFirst > 0 {
			e.failFirst--
			status = http.StatusServiceUnavailable
		}
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"## Alert Triage Report\nlooks benign"}}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	p, err := e.llmSvc.Create(t.Context(), e.tenantID, e.actorID, service.LLMProviderSaveInput{
		Name: "p", Kind: "openai_compatible", BaseURL: &srv.URL, Model: "m", APIKey: "sk", AutoAnalyzeAllAlerts: autoAnalyze,
	})
	require.NoError(t, err)
	require.NoError(t, e.llmSvc.SetDefault(t.Context(), e.tenantID, e.actorID, p.ID))
}

func (e *autoEnv) alert(t *testing.T) uuid.UUID {
	t.Helper()
	a, _, err := e.alertSvc.Ingest(t.Context(), e.tenantID, testutil.NewWebhookEndpoint(t, e.tenantID), domain.Alert{
		Title: "Suspicious login", Source: "wazuh", Severity: domain.SeverityHigh, Payload: testPayload,
	}, nil, 0)
	require.NoError(t, err)
	return a.ID
}

func (e *autoEnv) requestCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.requests)
}

func (e *autoEnv) setStatus(s int) {
	e.mu.Lock()
	e.status = s
	e.mu.Unlock()
}

// systemPromptOfLastRequest returns the system message the provider last received.
func (e *autoEnv) systemPromptOfLastRequest(t *testing.T) string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	require.NotEmpty(t, e.requests)
	msgs, _ := e.requests[len(e.requests)-1]["messages"].([]any)
	require.NotEmpty(t, msgs)
	first, _ := msgs[0].(map[string]any)
	require.Equal(t, "system", first["role"])
	content, _ := first["content"].(string)
	return content
}

// A synchronous analysis returns once it is finished, with the outcome as its
// error -- unlike StartAlertAnalysis, which returns at once and reports nothing.
func TestAIAnalysisService_RunAutoAnalysis_Succeeds(t *testing.T) {
	e := newAutoEnv(t)
	e.provider(t, true)
	alertID := e.alert(t)

	require.NoError(t, e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID))

	run := latestRun(t, e.pool, e.tenantID, "alert", alertID)
	require.NotNil(t, run)
	assert.Equal(t, domain.AIAnalysisRunCompleted, run.Status, "finished by the time it returned, no waiting for a goroutine")
	assert.Nil(t, run.ActorID, "the unattended trigger carries no actor")
	require.NotNil(t, run.Result)
	assert.Contains(t, *run.Result, "looks benign")

	select {
	case ev := <-e.analyzed:
		assert.Equal(t, "alert", ev, "an open alert page is told to reload")
	default:
		t.Fatal("the analysis finished but no live-update event was published")
	}
}

// The report the unattended pass produces is what "Analyze every incoming alert
// automatically" is for, and it is the one case that gets the triage prompt.
func TestAIAnalysisService_RunAutoAnalysis_UsesTheTriagePrompt(t *testing.T) {
	e := newAutoEnv(t)
	e.provider(t, true)
	alertID := e.alert(t)

	require.NoError(t, e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID))

	prompt := e.systemPromptOfLastRequest(t)
	assert.Contains(t, prompt, "## Alert Triage Report")
	assert.Contains(t, prompt, "PHASE 1 - COLLECT")
	assert.Contains(t, prompt, "[REDACTED]", "and it carries the injection/redaction rules")
}

func TestAIAnalysisService_RunAutoAnalysis_NothingToDo(t *testing.T) {
	t.Run("the provider has not opted in", func(t *testing.T) {
		e := newAutoEnv(t)
		e.provider(t, false)
		alertID := e.alert(t)

		err := e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID)
		assert.ErrorIs(t, err, service.ErrAutoAnalysisDisabled)
		assert.Zero(t, e.requestCount(), "the LLM must not be called")
		assert.Nil(t, latestRun(t, e.pool, e.tenantID, "alert", alertID), "and no run is created")
	})

	t.Run("no provider at all", func(t *testing.T) {
		e := newAutoEnv(t)
		alertID := e.alert(t)
		err := e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID)
		assert.ErrorIs(t, err, service.ErrNoLLMProvider)
		assert.ErrorContains(t, err, "no LLM provider configured", "the message analysts already saw is unchanged")
	})

	t.Run("the alert no longer exists", func(t *testing.T) {
		e := newAutoEnv(t)
		e.provider(t, true)
		missing := uuid.New()
		err := e.ai.RunAutoAnalysis(t.Context(), e.tenantID, missing)
		assert.ErrorIs(t, err, service.ErrSubjectNotFound)
		assert.ErrorContains(t, err, "alert "+missing.String()+" not found", "and so is this one")
	})
}

func TestAIAnalysisService_RunAutoAnalysis_FailureCarriesTheProviderError(t *testing.T) {
	t.Run("a provider outage is retryable", func(t *testing.T) {
		e := newAutoEnv(t)
		e.provider(t, true)
		e.setStatus(http.StatusServiceUnavailable)
		alertID := e.alert(t)

		err := e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID)
		require.Error(t, err)
		var pe *llmclient.ProviderError
		require.ErrorAs(t, err, &pe, "the queue classifies on this; it must survive the wrapping")
		assert.Equal(t, http.StatusServiceUnavailable, pe.Status)
		assert.True(t, llmclient.IsRetryable(err))

		assert.Greater(t, e.requestCount(), 1, "the provider call itself is retried in place before the analysis gives up")
		run := latestRun(t, e.pool, e.tenantID, "alert", alertID)
		assert.Equal(t, domain.AIAnalysisRunFailed, run.Status)
		require.NotNil(t, run.Error)
		assert.Contains(t, *run.Error, "temporarily unavailable")
		assert.True(t, strings.HasPrefix(*run.Error, "llm analysis: "), "the same prefix whichever path produced the failure")
	})

	t.Run("a rejected API key is not retryable", func(t *testing.T) {
		e := newAutoEnv(t)
		e.provider(t, true)
		e.setStatus(http.StatusUnauthorized)
		alertID := e.alert(t)

		err := e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID)
		require.Error(t, err)
		assert.False(t, llmclient.IsRetryable(err))
		assert.EqualValues(t, 1, e.requestCount(), "a 401 is not repeated")
	})

	t.Run("a second run is refused while one is in progress", func(t *testing.T) {
		e := newAutoEnv(t)
		e.provider(t, true)
		alertID := e.alert(t)
		// Leave a run behind in 'running', as a dead consumer would.
		require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			return repository.NewAIAnalysisRunRepository().Insert(t.Context(), tx, &domain.AIAnalysisRun{
				TenantID: e.tenantID, ContextType: "alert", ContextID: alertID, Status: domain.AIAnalysisRunRunning,
				Messages: json.RawMessage(`[]`), Tools: json.RawMessage(`[]`), ToolRoutes: json.RawMessage(`{}`),
			})
		}))

		err := e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID)
		assert.True(t, errors.Is(err, service.ErrAnalysisInProgress))
	})
}

// What the queue does when it takes a job over from a dead consumer: the run that
// consumer left 'running' would otherwise make the retry refuse to start.
func TestAIAnalysisService_AbandonInterruptedRuns(t *testing.T) {
	e := newAutoEnv(t)
	e.provider(t, true)
	alertID := e.alert(t)
	runs := repository.NewAIAnalysisRunRepository()

	insert := func(status domain.AIAnalysisRunStatus) int64 {
		var id int64
		require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			run := &domain.AIAnalysisRun{
				TenantID: e.tenantID, ContextType: "alert", ContextID: alertID, Status: domain.AIAnalysisRunRunning,
				Messages: json.RawMessage(`[]`), Tools: json.RawMessage(`[]`), ToolRoutes: json.RawMessage(`{}`),
			}
			if err := runs.Insert(t.Context(), tx, run); err != nil {
				return err
			}
			id = run.ID
			switch status {
			case domain.AIAnalysisRunCompleted:
				return runs.SetCompleted(t.Context(), tx, run.ID, json.RawMessage(`[]`), "done")
			case domain.AIAnalysisRunFailed:
				return runs.SetFailed(t.Context(), tx, run.ID, "earlier failure")
			}
			return nil
		}))
		return id
	}
	status := func(id int64) domain.AIAnalysisRunStatus {
		var s domain.AIAnalysisRunStatus
		require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			r, err := runs.Get(t.Context(), tx, id)
			if r != nil {
				s = r.Status
			}
			return err
		}))
		return s
	}

	completed := insert(domain.AIAnalysisRunCompleted)
	failed := insert(domain.AIAnalysisRunFailed)
	running := insert(domain.AIAnalysisRunRunning)

	n, err := e.ai.AbandonInterruptedRuns(t.Context(), e.tenantID, alertID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	assert.Equal(t, domain.AIAnalysisRunFailed, status(running))
	assert.Equal(t, domain.AIAnalysisRunCompleted, status(completed), "finished work is never touched")
	assert.Equal(t, domain.AIAnalysisRunFailed, status(failed))

	var cause *string
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		r, err := runs.Get(t.Context(), tx, running)
		if r != nil {
			cause = r.Error
		}
		return err
	}))
	require.NotNil(t, cause)
	assert.Contains(t, *cause, "interrupted")

	again, err := e.ai.AbandonInterruptedRuns(t.Context(), e.tenantID, alertID)
	require.NoError(t, err)
	assert.Zero(t, again, "idempotent: nothing left to abandon")

	// And with the stale run cleared, the retry can start.
	require.NoError(t, e.ai.RunAutoAnalysis(t.Context(), e.tenantID, alertID))
}

// The whole path with nothing faked but the LLM: an alert is ingested, its job is
// written in the same transaction, the queue claims it, the real service analyses
// it -- and when the provider is down for longer than the in-place retry covers,
// the job is rescheduled and the alert is still analysed once the provider is
// back. Before the queue, that alert would have been left unanalysed for good.
func TestAIAnalysisQueue_EndToEnd_ProviderOutageIsRiddenOut(t *testing.T) {
	e := newAutoEnv(t)
	testutil.ResetAIJobQueue(t)
	e.provider(t, true)
	// 4 failures: the first attempt's three in-place tries all fail (so the
	// attempt fails and the JOB is rescheduled), and the second attempt's first
	// try fails too before the provider recovers.
	e.mu.Lock()
	e.failFirst = 4
	e.mu.Unlock()

	jobs := repository.NewAIAnalysisJobRepository()
	e.alertSvc.EnableAutoAnalysisQueue(jobs, nil)

	// Ingest, as the webhook handler does -- nothing else triggers the analysis.
	alertID := e.alert(t)

	q := service.NewAIAnalysisQueue(e.pool, jobs, e.ai, service.AIAnalysisQueueConfig{
		Concurrency: 2, PollInterval: 20e6, Lease: 2e9, Backoff: []time.Duration{30 * time.Millisecond},
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	require.Eventually(t, func() bool {
		var st domain.AIAnalysisJobStatus
		_ = e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			j, err := jobs.GetByAlert(t.Context(), tx, alertID)
			if j != nil {
				st = j.Status
			}
			return err
		})
		return st == domain.AIAnalysisJobSucceeded
	}, 10*time.Second, 20*time.Millisecond, "the alert was never analysed")

	var job *domain.AIAnalysisJob
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		var err error
		job, err = jobs.GetByAlert(t.Context(), tx, alertID)
		return err
	}))
	assert.Equal(t, 2, job.Attempts, "one attempt lost to the outage, the next one analysed it")

	runs := e.runsFor(t, alertID)
	require.Len(t, runs, 2)
	assert.Equal(t, domain.AIAnalysisRunFailed, runs[0].Status, "the outage attempt left a failed run")
	assert.Equal(t, domain.AIAnalysisRunCompleted, runs[1].Status, "and the retry produced the analysis")
	require.NotNil(t, runs[1].Result)
	assert.Contains(t, *runs[1].Result, "looks benign")

	// The retry is the alert's first real analysis, so it still gets the triage
	// prompt: a failed attempt delivered nothing and must not use it up.
	assert.Contains(t, e.systemPromptOfLastRequest(t), "## Alert Triage Report")
}

func (e *autoEnv) runsFor(t *testing.T, alertID uuid.UUID) []domain.AIAnalysisRun {
	t.Helper()
	var runs []domain.AIAnalysisRun
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		var err error
		runs, err = repository.NewAIAnalysisRunRepository().ListByContext(t.Context(), tx, "alert", alertID)
		return err
	}))
	// Oldest first.
	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	if len(runs) > 1 && runs[0].ID > runs[1].ID {
		runs[0], runs[1] = runs[1], runs[0]
	}
	return runs
}
