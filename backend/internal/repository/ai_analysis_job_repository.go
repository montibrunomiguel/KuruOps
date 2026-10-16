package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
)

// AIAnalysisJobRepository is the SQL behind the auto-analysis queue (see
// domain.AIAnalysisJob).
//
// It has two very different kinds of caller. Enqueue runs inside an ordinary
// tenant-scoped transaction (the one that stores the alert) and is subject to
// row-level security like every other table. Everything else is the queue
// consumer, a single process working jobs for ALL tenants: it cannot know a
// job's tenant before reading the job, so those methods open the narrow
// queue_runner carve-out (see withQueueRunner) instead of going through
// Pool.WithTenant.
type AIAnalysisJobRepository struct{}

func NewAIAnalysisJobRepository() *AIAnalysisJobRepository { return &AIAnalysisJobRepository{} }

// Enqueue records that alertID is owed an automatic analysis, inside the
// caller's tenant transaction -- the same one that inserted the alert, which is
// the point: the obligation commits or rolls back together with the alert.
//
// It is a no-op (inserted=false) unless the tenant's default LLM provider has
// opted into "Analyze every incoming alert automatically", so a tenant that has
// not does not accumulate a row per alert only to have each one skipped. The
// consumer re-checks the setting when it runs the job, so turning it off later
// still stops work already queued.
//
// One job per alert: a repeated call for the same alert does nothing.
func (r *AIAnalysisJobRepository) Enqueue(ctx context.Context, tx pgx.Tx, tenantID, alertID uuid.UUID, maxAttempts int) (inserted bool, err error) {
	tag, err := tx.Exec(ctx, `
		insert into ai_analysis_jobs (tenant_id, alert_id, max_attempts)
		select $1, $2, $3
		where exists (select 1 from llm_providers where is_default and auto_analyze_all_alerts)
		on conflict (alert_id) do nothing`,
		tenantID, alertID, maxAttempts,
	)
	if err != nil {
		return false, fmt.Errorf("enqueue ai analysis job: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// withQueueRunner runs fn in a transaction that has the queue_runner policy
// (db/migrations/0014_ai_analysis_jobs.up.sql) switched on for its duration --
// the same transaction-local opt-in shape as WebhookRepository.ResolveToken's
// webhook_lookup. is_local=true scopes the setting to the transaction, so it can
// never leak onto a pooled connection another request reuses.
func (r *AIAnalysisJobRepository) withQueueRunner(ctx context.Context, pool *db.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	if _, err := tx.Exec(ctx, "select set_config('app.ai_queue_runner', 'true', true)"); err != nil {
		return fmt.Errorf("set queue runner context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Claim takes up to limit due jobs for this consumer and returns them.
//
// A job is due when it is pending and its next_attempt_at has passed, OR it is
// running but its lease (locked_until) has run out -- the consumer that held it
// died, and nothing else will ever finish it. Claiming adds one to attempts
// BEFORE any work starts, deliberately: a job that kills its consumer (an OOM, a
// panic that takes the process down) is counted each time, so it exhausts
// max_attempts instead of looping through restarts forever.
//
// FOR UPDATE SKIP LOCKED is what lets several consumers (ingest replicas) work
// the same queue without ever handing one job to two of them.
func (r *AIAnalysisJobRepository) Claim(ctx context.Context, pool *db.Pool, limit int, lease time.Duration) ([]domain.AIAnalysisJob, error) {
	if limit <= 0 {
		return nil, nil
	}
	var jobs []domain.AIAnalysisJob
	err := r.withQueueRunner(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			with due as (
				select id, (status = 'running') as was_running
				from ai_analysis_jobs
				where (status = 'pending' and next_attempt_at <= now())
				   or (status = 'running' and locked_until < now())
				order by next_attempt_at, id
				limit $1
				for update skip locked
			)
			update ai_analysis_jobs j
			set status = 'running',
			    attempts = j.attempts + 1,
			    locked_until = now() + ($2 * interval '1 second'),
			    updated_at = now()
			from due
			where j.id = due.id
			returning j.id, j.tenant_id, j.alert_id, j.attempts, j.max_attempts, j.created_at, due.was_running`,
			limit, lease.Seconds(),
		)
		if err != nil {
			return fmt.Errorf("claim ai analysis jobs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			j := domain.AIAnalysisJob{Status: domain.AIAnalysisJobRunning}
			if err := rows.Scan(&j.ID, &j.TenantID, &j.AlertID, &j.Attempts, &j.MaxAttempts, &j.CreatedAt, &j.Reclaimed); err != nil {
				return fmt.Errorf("scan ai analysis job: %w", err)
			}
			jobs = append(jobs, j)
		}
		return rows.Err()
	})
	return jobs, err
}

// The methods below that write a job's outcome (Extend, Mark*, Reschedule) take
// the claimed job, not just its id, and only apply if the row is STILL the one
// that claim produced: status 'running' and the same attempts number. attempts
// is a fencing token -- every Claim increments it, so a consumer whose lease
// lapsed (a long GC pause, a network partition from the database) and whose job
// was reclaimed by another consumer cannot, when it finally wakes up, overwrite
// the new owner's work with a stale outcome. A guard on status alone would not
// do that: the reclaimed row is 'running' too.
//
// Each reports whether it applied. false means the job is no longer this
// consumer's, which the caller should treat as "stop, someone else has it", not
// as an error.

// Extend pushes a running job's lease forward, so a consumer that is still alive
// and working keeps ownership of a job that outlasts one lease.
func (r *AIAnalysisJobRepository) Extend(ctx context.Context, pool *db.Pool, job domain.AIAnalysisJob, lease time.Duration) (bool, error) {
	return r.apply(ctx, pool, "extend ai analysis job lease", `
		update ai_analysis_jobs
		set locked_until = now() + ($3 * interval '1 second'), updated_at = now()
		where id = $1 and status = 'running' and attempts = $2`,
		job.ID, job.Attempts, lease.Seconds())
}

// finish moves a still-owned running job to a terminal status.
func (r *AIAnalysisJobRepository) finish(ctx context.Context, pool *db.Pool, job domain.AIAnalysisJob, status domain.AIAnalysisJobStatus, lastError string) (bool, error) {
	return r.apply(ctx, pool, "finish ai analysis job", `
		update ai_analysis_jobs
		set status = $3, locked_until = null, last_error = nullif($4, ''), updated_at = now()
		where id = $1 and status = 'running' and attempts = $2`,
		job.ID, job.Attempts, string(status), lastError)
}

func (r *AIAnalysisJobRepository) MarkSucceeded(ctx context.Context, pool *db.Pool, job domain.AIAnalysisJob) (bool, error) {
	return r.finish(ctx, pool, job, domain.AIAnalysisJobSucceeded, "")
}

// MarkSkipped records a job that has nothing to do and never will; reason is kept
// in last_error so "why is this not analysed" has an answer.
func (r *AIAnalysisJobRepository) MarkSkipped(ctx context.Context, pool *db.Pool, job domain.AIAnalysisJob, reason string) (bool, error) {
	return r.finish(ctx, pool, job, domain.AIAnalysisJobSkipped, reason)
}

func (r *AIAnalysisJobRepository) MarkFailed(ctx context.Context, pool *db.Pool, job domain.AIAnalysisJob, cause string) (bool, error) {
	return r.finish(ctx, pool, job, domain.AIAnalysisJobFailed, cause)
}

// Reschedule puts a still-owned running job back to pending, due after delay,
// remembering why. The delay is measured on the database's clock, not the
// consumer's, so a consumer whose clock is off cannot make a job due too early or
// never.
func (r *AIAnalysisJobRepository) Reschedule(ctx context.Context, pool *db.Pool, job domain.AIAnalysisJob, delay time.Duration, cause string) (bool, error) {
	return r.apply(ctx, pool, "reschedule ai analysis job", `
		update ai_analysis_jobs
		set status = 'pending', next_attempt_at = now() + ($3 * interval '1 second'),
		    locked_until = null, last_error = nullif($4, ''), updated_at = now()
		where id = $1 and status = 'running' and attempts = $2`,
		job.ID, job.Attempts, delay.Seconds(), cause)
}

// apply runs one fenced UPDATE under the queue_runner carve-out and reports
// whether it matched a row.
func (r *AIAnalysisJobRepository) apply(ctx context.Context, pool *db.Pool, what, sql string, args ...any) (bool, error) {
	var applied bool
	err := r.withQueueRunner(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, sql, args...)
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		applied = tag.RowsAffected() > 0
		return nil
	})
	return applied, err
}

// PruneFinished deletes jobs that reached a terminal state more than olderThan ago.
// The table is a work queue, not a history -- what happened to each analysis is
// on the alert's timeline and in ai_analysis_runs -- so finished rows are only
// useful for a short while, for diagnosing "why was this not analysed".
func (r *AIAnalysisJobRepository) PruneFinished(ctx context.Context, pool *db.Pool, olderThan time.Duration) (int64, error) {
	var n int64
	err := r.withQueueRunner(ctx, pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			delete from ai_analysis_jobs
			where status in ('succeeded', 'skipped', 'failed')
			  and updated_at < now() - ($1 * interval '1 second')`,
			olderThan.Seconds(),
		)
		if err != nil {
			return fmt.Errorf("prune ai analysis jobs: %w", err)
		}
		n = tag.RowsAffected()
		return nil
	})
	return n, err
}

// Get returns one job by id, or nil. For the consumer's own bookkeeping and tests.
func (r *AIAnalysisJobRepository) Get(ctx context.Context, pool *db.Pool, id int64) (*domain.AIAnalysisJob, error) {
	var job *domain.AIAnalysisJob
	err := r.withQueueRunner(ctx, pool, func(tx pgx.Tx) error {
		j, err := scanAIAnalysisJob(tx.QueryRow(ctx, aiAnalysisJobSelect+` where id = $1`, id))
		job = j
		return err
	})
	return job, err
}

// GetByAlert returns the job for an alert inside the caller's tenant
// transaction, or nil.
func (r *AIAnalysisJobRepository) GetByAlert(ctx context.Context, tx pgx.Tx, alertID uuid.UUID) (*domain.AIAnalysisJob, error) {
	return scanAIAnalysisJob(tx.QueryRow(ctx, aiAnalysisJobSelect+` where alert_id = $1`, alertID))
}

// Stats counts jobs by status and measures how long the oldest due job has waited,
// for the /metrics gauges.
func (r *AIAnalysisJobRepository) Stats(ctx context.Context, pool *db.Pool) (domain.AIAnalysisJobStats, error) {
	var st domain.AIAnalysisJobStats
	err := r.withQueueRunner(ctx, pool, func(tx pgx.Tx) error {
		var oldestSeconds float64
		err := tx.QueryRow(ctx, `
			select
			  count(*) filter (where status = 'pending'),
			  count(*) filter (where status = 'running'),
			  count(*) filter (where status = 'succeeded'),
			  count(*) filter (where status = 'failed'),
			  count(*) filter (where status = 'skipped'),
			  coalesce(extract(epoch from now() - min(next_attempt_at) filter (where status = 'pending' and next_attempt_at <= now())), 0)
			from ai_analysis_jobs`,
		).Scan(&st.Pending, &st.Running, &st.Succeeded, &st.Failed, &st.Skipped, &oldestSeconds)
		if err != nil {
			return fmt.Errorf("ai analysis job stats: %w", err)
		}
		st.OldestPendingAge = time.Duration(oldestSeconds * float64(time.Second))
		return nil
	})
	return st, err
}

const aiAnalysisJobSelect = `
	select id, tenant_id, alert_id, status, attempts, max_attempts, next_attempt_at,
	       locked_until, last_error, created_at, updated_at
	from ai_analysis_jobs`

func scanAIAnalysisJob(row pgx.Row) (*domain.AIAnalysisJob, error) {
	var j domain.AIAnalysisJob
	var status string
	err := row.Scan(&j.ID, &j.TenantID, &j.AlertID, &status, &j.Attempts, &j.MaxAttempts, &j.NextAttemptAt,
		&j.LockedUntil, &j.LastError, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan ai analysis job: %w", err)
	}
	j.Status = domain.AIAnalysisJobStatus(status)
	return &j, nil
}
