package service_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// fakeAnalyzer stands in for AIAnalysisService so the queue's own behaviour --
// claiming, retrying, leasing, draining -- is observable without an LLM.
type fakeAnalyzer struct {
	mu        sync.Mutex
	calls     map[uuid.UUID]int // per alert
	abandoned map[uuid.UUID]int
	// run decides each call's outcome; the default succeeds.
	run func(ctx context.Context, tenantID, alertID uuid.UUID, call int) error

	inFlight, maxInFlight atomic.Int32
}

func newFakeAnalyzer() *fakeAnalyzer {
	return &fakeAnalyzer{calls: map[uuid.UUID]int{}, abandoned: map[uuid.UUID]int{}}
}

func (f *fakeAnalyzer) RunAutoAnalysis(ctx context.Context, tenantID, alertID uuid.UUID) error {
	f.mu.Lock()
	f.calls[alertID]++
	call := f.calls[alertID]
	run := f.run
	f.mu.Unlock()

	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxInFlight.Load()
		if n <= m || f.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	if run == nil {
		return nil
	}
	return run(ctx, tenantID, alertID, call)
}

func (f *fakeAnalyzer) AbandonInterruptedRuns(_ context.Context, _, alertID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abandoned[alertID]++
	return 1, nil
}

func (f *fakeAnalyzer) callsFor(id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

func (f *fakeAnalyzer) abandonsFor(id uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.abandoned[id]
}

type queueEnv struct {
	pool     *db.Pool
	jobs     *repository.AIAnalysisJobRepository
	tenantID uuid.UUID
}

func newQueueEnv(t *testing.T) *queueEnv {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	testutil.ResetAIJobQueue(t)
	e := &queueEnv{pool: pool, jobs: repository.NewAIAnalysisJobRepository(), tenantID: testutil.NewTenant(t)}
	require.NoError(t, pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		return repository.NewLLMProviderRepository().Insert(t.Context(), tx, &domain.LLMProvider{
			TenantID: e.tenantID, Name: "p", Kind: "openai_compatible", Model: "m",
			APIKeySecretRef: "ref", IsDefault: true, AutoAnalyzeAllAlerts: true,
		})
	}))
	return e
}

// enqueue stores an alert and its job the way Ingest does.
func (e *queueEnv) enqueue(t *testing.T, maxAttempts int) uuid.UUID {
	t.Helper()
	var alertID uuid.UUID
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		a := &domain.Alert{
			TenantID: e.tenantID, Title: "t", Source: "s", Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh,
			Status: domain.AlertStatusOpen, Tags: []string{}, Payload: []byte(`{}`), ReceivedAt: time.Now(),
		}
		if err := repository.NewAlertRepository().Insert(t.Context(), tx, a); err != nil {
			return err
		}
		alertID = a.ID
		_, err := e.jobs.Enqueue(t.Context(), tx, e.tenantID, a.ID, maxAttempts)
		return err
	}))
	return alertID
}

func (e *queueEnv) job(t *testing.T, alertID uuid.UUID) *domain.AIAnalysisJob {
	t.Helper()
	var j *domain.AIAnalysisJob
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		var err error
		j, err = e.jobs.GetByAlert(t.Context(), tx, alertID)
		return err
	}))
	return j
}

func (e *queueEnv) waitFor(t *testing.T, alertID uuid.UUID, status domain.AIAnalysisJobStatus) *domain.AIAnalysisJob {
	t.Helper()
	require.Eventually(t, func() bool {
		j := e.job(t, alertID)
		return j != nil && j.Status == status
	}, 8*time.Second, 15*time.Millisecond, "job never reached %q (now %v)", status, func() any {
		if j := e.job(t, alertID); j != nil {
			return j.Status
		}
		return nil
	}())
	return e.job(t, alertID)
}

// fastCfg keeps every wait in the tens of milliseconds.
func fastCfg() service.AIAnalysisQueueConfig {
	return service.AIAnalysisQueueConfig{
		Concurrency: 5, PollInterval: 20 * time.Millisecond, Lease: 600 * time.Millisecond, HeartbeatEvery: 150 * time.Millisecond,
		Backoff: []time.Duration{20 * time.Millisecond, 20 * time.Millisecond}, DrainTimeout: time.Second,
	}
}

// start runs the queue until the test ends (or stop is called) and returns a
// func that stops it and waits for Run to return.
func (e *queueEnv) start(t *testing.T, a service.AutoAnalyzer, cfg service.AIAnalysisQueueConfig) (q *service.AIAnalysisQueue, stop func()) {
	t.Helper()
	q = service.NewAIAnalysisQueue(e.pool, e.jobs, a, cfg, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("queue did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return q, stop
}

func TestAIAnalysisQueue_AnalysesAQueuedAlert(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	alertID := e.enqueue(t, 5)
	e.start(t, fa, fastCfg())

	j := e.waitFor(t, alertID, domain.AIAnalysisJobSucceeded)
	assert.Equal(t, 1, fa.callsFor(alertID))
	assert.Equal(t, 1, j.Attempts)
	assert.Nil(t, j.LastError)
}

// The case this whole change exists for: the provider fails the first time, and
// the alert is still analysed -- before, that was the end of it.
func TestAIAnalysisQueue_RetriesATransientFailureUntilItSucceeds(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	fa.run = func(_ context.Context, _, _ uuid.UUID, call int) error {
		if call <= 2 {
			return &llmclient.ProviderError{Status: 503, Message: "the AI provider is temporarily unavailable (503)"}
		}
		return nil
	}
	alertID := e.enqueue(t, 5)
	e.start(t, fa, fastCfg())

	j := e.waitFor(t, alertID, domain.AIAnalysisJobSucceeded)
	assert.Equal(t, 3, fa.callsFor(alertID))
	assert.Equal(t, 3, j.Attempts)
}

func TestAIAnalysisQueue_GivesUpAfterMaxAttempts(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	fa.run = func(context.Context, uuid.UUID, uuid.UUID, int) error {
		return &llmclient.ProviderError{Status: 503, Message: "the AI provider is temporarily unavailable (503)"}
	}
	alertID := e.enqueue(t, 3)
	e.start(t, fa, fastCfg())

	j := e.waitFor(t, alertID, domain.AIAnalysisJobFailed)
	assert.Equal(t, 3, fa.callsFor(alertID), "exactly max_attempts tries, no more")
	require.NotNil(t, j.LastError)
	assert.Contains(t, *j.LastError, "gave up after 3 attempts")
	assert.Contains(t, *j.LastError, "temporarily unavailable", "and says why")
}

func TestAIAnalysisQueue_DoesNotRetryAFailureThatWillRepeat(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	fa.run = func(context.Context, uuid.UUID, uuid.UUID, int) error {
		return &llmclient.ProviderError{Status: 401, Message: "the AI provider rejected the credentials (401)"}
	}
	alertID := e.enqueue(t, 5)
	e.start(t, fa, fastCfg())

	j := e.waitFor(t, alertID, domain.AIAnalysisJobFailed)
	assert.Equal(t, 1, fa.callsFor(alertID), "a bad API key is not fixed by asking again")
	require.NotNil(t, j.LastError)
	assert.Contains(t, *j.LastError, "rejected the credentials")
}

func TestAIAnalysisQueue_SkipsWhenThereIsNothingToDo(t *testing.T) {
	for name, cause := range map[string]error{
		"auto-analysis turned off":   service.ErrAutoAnalysisDisabled,
		"no provider":                service.ErrNoLLMProvider,
		"alert deleted before run":   errors.Join(service.ErrSubjectNotFound),
		"wrapped, as prepare wraps":  errors.Join(errors.New("prepare"), service.ErrAutoAnalysisDisabled),
		"skip is not an error state": service.ErrAutoAnalysisDisabled,
	} {
		t.Run(name, func(t *testing.T) {
			e := newQueueEnv(t)
			fa := newFakeAnalyzer()
			fa.run = func(context.Context, uuid.UUID, uuid.UUID, int) error { return cause }
			alertID := e.enqueue(t, 5)
			e.start(t, fa, fastCfg())

			j := e.waitFor(t, alertID, domain.AIAnalysisJobSkipped)
			assert.Equal(t, 1, fa.callsFor(alertID))
			assert.NotNil(t, j.LastError, "the reason is kept")
		})
	}
}

func TestAIAnalysisQueue_RetriesWhenAnotherAnalysisIsStillRunning(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	fa.run = func(_ context.Context, _, _ uuid.UUID, call int) error {
		if call == 1 {
			return service.ErrAnalysisInProgress
		}
		return nil
	}
	alertID := e.enqueue(t, 5)
	e.start(t, fa, fastCfg())

	e.waitFor(t, alertID, domain.AIAnalysisJobSucceeded)
	assert.Equal(t, 2, fa.callsFor(alertID))
}

// Concurrency is bounded per process: a burst of alerts must not become a burst of
// simultaneous LLM calls.
func TestAIAnalysisQueue_NeverRunsMoreThanTheConcurrencyLimit(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	release := make(chan struct{})
	fa.run = func(ctx context.Context, _, _ uuid.UUID, _ int) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	var ids []uuid.UUID
	for i := 0; i < 8; i++ {
		ids = append(ids, e.enqueue(t, 5))
	}
	cfg := fastCfg()
	cfg.Concurrency = 3
	e.start(t, fa, cfg)

	require.Eventually(t, func() bool { return fa.inFlight.Load() == 3 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond) // give it every chance to over-run
	assert.EqualValues(t, 3, fa.maxInFlight.Load(), "never more than Concurrency at once")

	close(release)
	for _, id := range ids {
		e.waitFor(t, id, domain.AIAnalysisJobSucceeded)
	}
	assert.EqualValues(t, 3, fa.maxInFlight.Load())
}

// A job that outlasts its lease must keep it: the heartbeat renews the lease for
// as long as the analysis is genuinely running, so a slow-but-alive consumer is
// not mistaken for a dead one and its alert analysed twice.
func TestAIAnalysisQueue_HeartbeatKeepsALongJobFromBeingTakenOver(t *testing.T) {
	e := newQueueEnv(t)
	slow := newFakeAnalyzer()
	release := make(chan struct{})
	slow.run = func(ctx context.Context, _, _ uuid.UUID, _ int) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	rival := newFakeAnalyzer()
	alertID := e.enqueue(t, 5)

	cfg := fastCfg() // lease 600ms
	e.start(t, slow, cfg)
	require.Eventually(t, func() bool { return slow.callsFor(alertID) == 1 }, 5*time.Second, 10*time.Millisecond)
	e.start(t, rival, cfg) // a second consumer, polling the same queue

	time.Sleep(3 * cfg.Lease) // three full leases with the job still running
	assert.Zero(t, rival.callsFor(alertID), "the live consumer's job must not be handed to another")
	assert.Equal(t, 1, slow.callsFor(alertID))

	close(release)
	e.waitFor(t, alertID, domain.AIAnalysisJobSucceeded)
	assert.Zero(t, rival.callsFor(alertID))
}

// The crash case: a consumer claims a job and dies. Nothing renews the lease, so
// after it runs out the next consumer takes the job over, clears the half-finished
// run the dead one left behind, and analyses the alert.
func TestAIAnalysisQueue_TakesOverAJobFromAConsumerThatDied(t *testing.T) {
	e := newQueueEnv(t)
	alertID := e.enqueue(t, 5)

	// The "dead consumer": claims with a short lease and is never heard from again.
	claimed, err := e.jobs.Claim(t.Context(), e.pool, 100, 300*time.Millisecond)
	require.NoError(t, err)
	var dead *domain.AIAnalysisJob
	for i := range claimed {
		if claimed[i].AlertID == alertID {
			dead = &claimed[i]
		}
	}
	require.NotNil(t, dead)
	require.Equal(t, domain.AIAnalysisJobRunning, e.job(t, alertID).Status)

	fa := newFakeAnalyzer()
	e.start(t, fa, fastCfg())

	j := e.waitFor(t, alertID, domain.AIAnalysisJobSucceeded)
	assert.Equal(t, 1, fa.callsFor(alertID), "analysed once, by the new consumer")
	assert.Equal(t, 1, fa.abandonsFor(alertID), "the run the dead consumer left behind must be cleared first, or it would block this attempt")
	assert.Equal(t, 2, j.Attempts, "the dead consumer's attempt counts")

	applied, err := e.jobs.MarkFailed(t.Context(), e.pool, *dead, "the dead consumer waking up late")
	require.NoError(t, err)
	assert.False(t, applied, "and if the old consumer ever wakes, it cannot overwrite the result")
	assert.Equal(t, domain.AIAnalysisJobSucceeded, e.job(t, alertID).Status)
}

// A job that kills its consumer every time (an out-of-memory on a pathological
// payload) must not loop forever through restarts. Each claim counts an attempt
// before any work, so it reaches the limit without ever reporting an outcome.
func TestAIAnalysisQueue_AJobThatKeepsKillingItsConsumerIsGivenUpOn(t *testing.T) {
	e := newQueueEnv(t)
	alertID := e.enqueue(t, 2)

	for i := 0; i < 3; i++ { // three claims, each followed by a "crash"
		claimed, err := e.jobs.Claim(t.Context(), e.pool, 100, 50*time.Millisecond)
		require.NoError(t, err)
		_ = claimed
		time.Sleep(80 * time.Millisecond)
	}
	require.Equal(t, 3, e.job(t, alertID).Attempts)

	fa := newFakeAnalyzer()
	e.start(t, fa, fastCfg())

	j := e.waitFor(t, alertID, domain.AIAnalysisJobFailed)
	assert.Zero(t, fa.callsFor(alertID), "no fourth attempt at a job that has already exhausted its budget")
	require.NotNil(t, j.LastError)
	assert.Contains(t, *j.LastError, "gave up after 2 attempts")
}

func TestAIAnalysisQueue_WakeProcessesAJobWithoutWaitingForThePoll(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	cfg := fastCfg()
	cfg.PollInterval = time.Hour // only a wake can make this prompt
	q, _ := e.start(t, fa, cfg)
	time.Sleep(100 * time.Millisecond) // let the first (empty) poll happen

	alertID := e.enqueue(t, 5)
	q.Wake()
	e.waitFor(t, alertID, domain.AIAnalysisJobSucceeded)
}

// On shutdown an analysis that is about to finish is allowed to, instead of every
// one being aborted mid-LLM-call and paid for again after the restart.
func TestAIAnalysisQueue_ShutdownLetsInFlightAnalysesFinish(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	started := make(chan struct{})
	fa.run = func(ctx context.Context, _, _ uuid.UUID, _ int) error {
		close(started)
		select {
		case <-time.After(250 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	alertID := e.enqueue(t, 5)
	_, stop := e.start(t, fa, fastCfg())

	<-started
	stop() // returns only once Run has drained

	assert.Equal(t, domain.AIAnalysisJobSucceeded, e.job(t, alertID).Status, "the in-flight analysis finished rather than being cut off")
}

// If it does not finish within the drain window, it is cancelled and the job is
// handed straight back, so whichever consumer starts next picks it up at once
// instead of waiting out a lease.
func TestAIAnalysisQueue_ShutdownReschedulesWhatCannotFinishInTime(t *testing.T) {
	e := newQueueEnv(t)
	fa := newFakeAnalyzer()
	started := make(chan struct{})
	fa.run = func(ctx context.Context, _, _ uuid.UUID, _ int) error {
		close(started)
		<-ctx.Done() // never finishes on its own
		return ctx.Err()
	}
	alertID := e.enqueue(t, 5)
	cfg := fastCfg()
	cfg.DrainTimeout = 150 * time.Millisecond
	_, stop := e.start(t, fa, cfg)

	<-started
	begin := time.Now()
	stop()
	assert.Less(t, time.Since(begin), 3*time.Second, "Run returns once the drain window has passed")

	j := e.job(t, alertID)
	assert.Equal(t, domain.AIAnalysisJobPending, j.Status, "handed back, not left 'running' until its lease lapses")
	require.NotNil(t, j.LastError)
	assert.Contains(t, *j.LastError, "shutting down")
	assert.False(t, j.NextAttemptAt.After(time.Now().Add(time.Second)), "due immediately")
}

func TestAIAnalysisQueue_ClaimsAcrossTenants(t *testing.T) {
	a, b := newQueueEnv(t), newQueueEnv(t)
	aAlert, bAlert := a.enqueue(t, 5), b.enqueue(t, 5)
	fa := newFakeAnalyzer()
	seen := map[uuid.UUID]uuid.UUID{} // alert -> tenant the analyzer was given
	var mu sync.Mutex
	fa.run = func(_ context.Context, tenantID, alertID uuid.UUID, _ int) error {
		mu.Lock()
		seen[alertID] = tenantID
		mu.Unlock()
		return nil
	}
	a.start(t, fa, fastCfg())

	a.waitFor(t, aAlert, domain.AIAnalysisJobSucceeded)
	b.waitFor(t, bAlert, domain.AIAnalysisJobSucceeded)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, a.tenantID, seen[aAlert])
	assert.Equal(t, b.tenantID, seen[bAlert], "one consumer works every tenant's jobs, each with its own tenant id")
}
