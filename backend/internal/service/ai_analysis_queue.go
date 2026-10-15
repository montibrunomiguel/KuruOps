package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/safego"
)

// AutoAnalyzer is what the queue needs from AIAnalysisService. An interface so
// the queue's scheduling and retry rules can be tested without a database, an
// LLM or real waiting.
type AutoAnalyzer interface {
	RunAutoAnalysis(ctx context.Context, tenantID, alertID uuid.UUID) error
	AbandonInterruptedRuns(ctx context.Context, tenantID, alertID uuid.UUID) (int64, error)
}

// DefaultAnalysisJobMaxAttempts is how many times a job is tried before the
// queue gives up on it. Each try already retries the LLM call in place
// (llmclient), so this is retries of whole analyses over a longer horizon:
// with DefaultAnalysisBackoff, about an hour and a half of provider outage is
// ridden out before an alert is left unanalysed.
const DefaultAnalysisJobMaxAttempts = 5

// DefaultAnalysisBackoff is how long a job waits after its Nth failed attempt:
// a minute, five, fifteen, an hour. Long enough that a provider that is
// genuinely down is not hammered, short enough that a brief outage costs an
// alert a few minutes of waiting, not hours.
var DefaultAnalysisBackoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// AIAnalysisQueueConfig tunes the consumer. The zero value is valid: every field
// falls back to the default noted on it.
type AIAnalysisQueueConfig struct {
	// Concurrency is how many analyses one process runs at once (default 5). It
	// replaces the semaphore AlertService used to hold: a burst of alerts far
	// outpaces an LLM call, and an unbounded fan-out is a cost, rate-limit and
	// resource problem for this process and the provider alike. Per replica, so
	// N ingest replicas run up to N times this.
	Concurrency int
	// PollInterval is how often an idle consumer looks for due jobs (default 5s).
	// A job enqueued in this process wakes it immediately (see Wake), so this only
	// bounds how late a job enqueued by ANOTHER replica, or one whose backoff has
	// just elapsed, is noticed.
	PollInterval time.Duration
	// Lease is how long a claimed job stays owned without a heartbeat (default
	// 2m). It is the time after which a dead consumer's work is taken over, so it
	// bounds how long a crash delays an analysis.
	Lease time.Duration
	// HeartbeatEvery is how often a running job's lease is renewed (default
	// Lease/3), so a job can run for as long as it needs while its consumer lives.
	HeartbeatEvery time.Duration
	// Backoff is the wait after the Nth failed attempt (default
	// DefaultAnalysisBackoff; the last entry repeats). It gets +-20% jitter.
	Backoff []time.Duration
	// PruneEvery / PruneAfter: finished jobs older than PruneAfter (default 14
	// days) are deleted, checked every PruneEvery (default 1h).
	PruneEvery time.Duration
	PruneAfter time.Duration
	// DrainTimeout is how long Run waits for in-flight analyses to finish after
	// being asked to stop (default 20s). Past that they are cancelled and
	// rescheduled, to be picked up at once by whichever consumer runs next.
	DrainTimeout time.Duration
}

func (c AIAnalysisQueueConfig) withDefaults() AIAnalysisQueueConfig {
	if c.Concurrency <= 0 {
		c.Concurrency = 5
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.Lease <= 0 {
		c.Lease = 2 * time.Minute
	}
	if c.HeartbeatEvery <= 0 {
		c.HeartbeatEvery = c.Lease / 3
	}
	if len(c.Backoff) == 0 {
		c.Backoff = DefaultAnalysisBackoff
	}
	if c.PruneEvery <= 0 {
		c.PruneEvery = time.Hour
	}
	if c.PruneAfter <= 0 {
		c.PruneAfter = 14 * 24 * time.Hour
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = 20 * time.Second
	}
	return c
}

// AIAnalysisQueue works the durable queue of automatic alert analyses (see
// domain.AIAnalysisJob and db/migrations/0014_ai_analysis_jobs.up.sql).
//
// The analysis used to be a goroutine started by Ingest the moment an alert
// landed. That lost work in two ways: a restart or crash between "alert stored"
// and "analysis finished" dropped it with no trace, and a provider error ended it
// for good because nothing was ever going to try again. Now the obligation is a
// row written in the alert's own transaction, and this consumer works through the
// rows with a lease (so a dead consumer's jobs are taken over), a fencing token
// (so a stalled one cannot overwrite its successor), and retry with backoff.
//
// It is safe to run in several processes against one database: jobs are claimed
// with FOR UPDATE SKIP LOCKED, so no job goes to two of them.
type AIAnalysisQueue struct {
	pool     *db.Pool
	jobs     *repository.AIAnalysisJobRepository
	analyzer AutoAnalyzer
	cfg      AIAnalysisQueueConfig
	logger   *slog.Logger

	wake      chan struct{} // an enqueue happened in this process
	slotFreed chan struct{} // a running job finished, so a claim may now have room
	lastPrune time.Time
}

func NewAIAnalysisQueue(pool *db.Pool, jobs *repository.AIAnalysisJobRepository, analyzer AutoAnalyzer, cfg AIAnalysisQueueConfig, logger *slog.Logger) *AIAnalysisQueue {
	if logger == nil {
		logger = slog.Default()
	}
	return &AIAnalysisQueue{
		pool: pool, jobs: jobs, analyzer: analyzer, cfg: cfg.withDefaults(), logger: logger,
		wake: make(chan struct{}, 1), slotFreed: make(chan struct{}, 1),
	}
}

// Wake tells an idle consumer that a job was just enqueued, so it does not wait
// out PollInterval. Never blocks; a wake while one is already pending is dropped,
// since the pending one will find the job.
func (q *AIAnalysisQueue) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Stats reports the queue's depth and the age of its oldest due job, for /metrics.
func (q *AIAnalysisQueue) Stats(ctx context.Context) (domain.AIAnalysisJobStats, error) {
	return q.jobs.Stats(ctx, q.pool)
}

// Run works the queue until ctx is cancelled, then lets in-flight analyses finish
// (up to DrainTimeout) and returns. It claims only as many jobs as it has free
// slots, so a backlog queues in the database, not in this process's memory.
func (q *AIAnalysisQueue) Run(ctx context.Context) {
	// In-flight analyses run on a context of their own, not on ctx: shutting the
	// process down should let a nearly finished analysis finish, not abort every
	// one of them mid-LLM-call and pay for the call again on restart.
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	var wg sync.WaitGroup
	slots := make(chan struct{}, q.cfg.Concurrency)

	for ctx.Err() == nil {
		free := cap(slots) - len(slots)
		claimedAll := false
		if free > 0 {
			jobs, err := q.jobs.Claim(ctx, q.pool, free, q.cfg.Lease)
			switch {
			case err != nil && ctx.Err() == nil:
				q.logger.Warn("ai analysis queue: claiming jobs failed", "error", err)
			case err == nil:
				for _, job := range jobs {
					slots <- struct{}{}
					wg.Add(1)
					safego.Go("ai-analysis-queue.process", func() {
						defer func() {
							<-slots
							wg.Done()
							select {
							case q.slotFreed <- struct{}{}:
							default:
							}
						}()
						q.process(runCtx, job)
					})
				}
				claimedAll = len(jobs) == free
			}
		}
		q.pruneIfDue(ctx)

		if claimedAll {
			continue // the batch was full: more may be due right now
		}
		select {
		case <-ctx.Done():
		case <-q.wake:
		case <-q.slotFreed:
		case <-time.After(q.cfg.PollInterval):
		}
	}

	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(q.cfg.DrainTimeout):
		q.logger.Warn("ai analysis queue: in-flight analyses did not finish in time, cancelling them", "timeout", q.cfg.DrainTimeout)
		cancelRun() // process() reschedules whatever it is cancelled out of
		<-drained
	}
}

func (q *AIAnalysisQueue) pruneIfDue(ctx context.Context) {
	if time.Since(q.lastPrune) < q.cfg.PruneEvery {
		return
	}
	q.lastPrune = time.Now()
	if n, err := q.jobs.PruneFinished(ctx, q.pool, q.cfg.PruneAfter); err != nil {
		if ctx.Err() == nil {
			q.logger.Warn("ai analysis queue: pruning finished jobs failed", "error", err)
		}
	} else if n > 0 {
		q.logger.Info("ai analysis queue: pruned finished jobs", "count", n)
	}
}

// process runs one claimed job to an outcome. Every write of that outcome goes
// through the job's fencing token, so if this consumer stalled past its lease and
// the job was taken over, the write is dropped instead of clobbering the new
// owner -- and the heartbeat, which learns of the takeover first, has already
// cancelled the analysis.
func (q *AIAnalysisQueue) process(runCtx context.Context, job domain.AIAnalysisJob) {
	log := q.logger.With("job_id", job.ID, "alert_id", job.AlertID, "tenant_id", job.TenantID, "attempt", job.Attempts, "max_attempts", job.MaxAttempts)

	// A job that kills its consumer is counted at every claim, so it reaches here
	// with attempts past the limit once it has used them all.
	if job.Attempts > job.MaxAttempts {
		q.finish(runCtx, log, job, func(ctx context.Context) (bool, error) {
			return q.jobs.MarkFailed(ctx, q.pool, job, fmt.Sprintf("gave up after %d attempts: earlier attempts ended without reporting an outcome (the process running them was stopped)", job.MaxAttempts))
		})
		return
	}

	ctx, cancel := context.WithCancel(runCtx)
	defer cancel()
	stopHeartbeat := q.heartbeat(ctx, cancel, log, job)
	defer stopHeartbeat()

	if job.Reclaimed {
		// Taken over from a consumer that died. The run it left behind will never
		// finish and would make this attempt refuse to start.
		if n, err := q.analyzer.AbandonInterruptedRuns(ctx, job.TenantID, job.AlertID); err != nil {
			log.Warn("ai analysis queue: could not clear the interrupted run", "error", err)
		} else if n > 0 {
			log.Info("ai analysis queue: took over an interrupted analysis", "interrupted_runs", n)
		}
	}

	err := q.analyzer.RunAutoAnalysis(ctx, job.TenantID, job.AlertID)

	// Cancelled out from under us -- the drain timeout at shutdown, or the lease
	// was lost. Record nothing as an outcome: hand the job back to be picked up at
	// once (shutdown) or leave it to the new owner (lost lease; the write below is
	// fenced and will be a no-op).
	if err != nil && runCtx.Err() != nil {
		q.finish(runCtx, log, job, func(ctx context.Context) (bool, error) {
			return q.jobs.Reschedule(ctx, q.pool, job, 0, "interrupted: the process handling this analysis was shutting down")
		})
		return
	}

	switch outcome := q.classify(job, err); outcome.kind {
	case outcomeSucceeded:
		q.finish(runCtx, log, job, func(ctx context.Context) (bool, error) { return q.jobs.MarkSucceeded(ctx, q.pool, job) })
		log.Info("ai analysis queue: alert analysed")
	case outcomeSkipped:
		q.finish(runCtx, log, job, func(ctx context.Context) (bool, error) { return q.jobs.MarkSkipped(ctx, q.pool, job, outcome.reason) })
		log.Debug("ai analysis queue: nothing to do", "reason", outcome.reason)
	case outcomeFailed:
		q.finish(runCtx, log, job, func(ctx context.Context) (bool, error) { return q.jobs.MarkFailed(ctx, q.pool, job, outcome.reason) })
		log.Warn("ai analysis queue: giving up on this alert", "reason", outcome.reason)
	case outcomeRetry:
		q.finish(runCtx, log, job, func(ctx context.Context) (bool, error) {
			return q.jobs.Reschedule(ctx, q.pool, job, outcome.delay, outcome.reason)
		})
		log.Warn("ai analysis queue: analysis failed, will retry", "retry_in", outcome.delay, "reason", outcome.reason)
	}
}

// finish writes a job's outcome. It uses its own short context rather than the
// analysis's: the analysis context is exactly what is cancelled in the cases
// where an outcome still has to be recorded.
func (q *AIAnalysisQueue) finish(_ context.Context, log *slog.Logger, job domain.AIAnalysisJob, write func(ctx context.Context) (bool, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	applied, err := write(ctx)
	switch {
	case err != nil:
		// Nothing more to do: the job stays 'running' and its lease will run out,
		// at which point it is claimed again -- which is the safe direction.
		log.Error("ai analysis queue: could not record the outcome; the job will be picked up again when its lease expires", "error", err)
	case !applied:
		log.Warn("ai analysis queue: the job is no longer owned by this consumer, outcome dropped")
	}
}

// heartbeat renews the job's lease while it runs and returns a func that stops
// it. If a renewal reports the job is no longer ours, the analysis is cancelled:
// another consumer owns it now and a second analysis of the same alert would just
// be duplicated cost.
func (q *AIAnalysisQueue) heartbeat(ctx context.Context, cancel context.CancelFunc, log *slog.Logger, job domain.AIAnalysisJob) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(q.cfg.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				renewCtx, renewCancel := context.WithTimeout(context.Background(), 10*time.Second)
				ok, err := q.jobs.Extend(renewCtx, q.pool, job, q.cfg.Lease)
				renewCancel()
				switch {
				case err != nil:
					// A database blip: keep working, the next beat retries, and the
					// lease still has most of its length left.
					log.Warn("ai analysis queue: could not renew the job lease", "error", err)
				case !ok:
					log.Warn("ai analysis queue: lost the job lease, abandoning this analysis")
					cancel()
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

type outcomeKind int

const (
	outcomeSucceeded outcomeKind = iota
	outcomeSkipped
	outcomeFailed
	outcomeRetry
)

type jobOutcome struct {
	kind   outcomeKind
	reason string
	delay  time.Duration // for outcomeRetry
}

// classify turns an analysis's error into what to do with its job.
//
//   - nil: done.
//   - Nothing to do, ever (auto-analysis switched off, no provider, the alert was
//     deleted): skipped, not an error. These are normal states, not failures.
//   - Will fail the same way every time (a rejected API key, a wrong URL or
//     model, a blocked destination): failed at once. Retrying a 401 only delays
//     the message and burns the provider's rate limit; the operator has to act.
//   - Everything else -- a provider outage or rate limit, an open circuit
//     breaker, another analysis still running, a database blip, anything
//     unrecognised -- is worth trying again later, with backoff, until
//     max_attempts. Unknown errors lean to retry: a wrongly retried job costs
//     a few minutes, a wrongly abandoned one is an alert nobody analysed.
func (q *AIAnalysisQueue) classify(job domain.AIAnalysisJob, err error) jobOutcome {
	var pe *llmclient.ProviderError
	var te *llmclient.TransportError
	switch {
	case err == nil:
		return jobOutcome{kind: outcomeSucceeded}
	case errors.Is(err, ErrAutoAnalysisDisabled):
		return jobOutcome{kind: outcomeSkipped, reason: "auto-analysis is switched off for the default LLM provider"}
	case errors.Is(err, ErrNoLLMProvider):
		return jobOutcome{kind: outcomeSkipped, reason: "no LLM provider is configured"}
	case errors.Is(err, ErrSubjectNotFound):
		return jobOutcome{kind: outcomeSkipped, reason: "the alert no longer exists"}
	case errors.As(err, &pe) && !pe.Retryable(),
		errors.As(err, &te) && !te.Retryable() && !errors.Is(err, context.Canceled):
		return jobOutcome{kind: outcomeFailed, reason: err.Error()}
	}

	if job.Attempts >= job.MaxAttempts {
		return jobOutcome{kind: outcomeFailed, reason: fmt.Sprintf("gave up after %d attempts: %v", job.Attempts, err)}
	}
	return jobOutcome{kind: outcomeRetry, reason: err.Error(), delay: q.backoff(job.Attempts)}
}

// backoff is the wait after the Nth failed attempt, with +-20% jitter so a burst
// of jobs that failed together (the provider went down) do not all come due, and
// hit the recovering provider, at the same instant.
func (q *AIAnalysisQueue) backoff(attempt int) time.Duration {
	i := max(attempt-1, 0)
	if i >= len(q.cfg.Backoff) {
		i = len(q.cfg.Backoff) - 1
	}
	d := q.cfg.Backoff[i]
	jitter := time.Duration(float64(d) * 0.2 * (rand.Float64()*2 - 1))
	return d + jitter
}
