package repository_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

type jobEnv struct {
	pool     *db.Pool
	repo     *repository.AIAnalysisJobRepository
	alerts   *repository.AlertRepository
	tenantID uuid.UUID
}

// newJobEnv builds a tenant whose default LLM provider has auto-analysis set to
// autoOn, which is what decides whether Enqueue inserts anything.
func newJobEnv(t *testing.T, autoOn bool) *jobEnv {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	testutil.ResetAIJobQueue(t)
	env := &jobEnv{
		pool: pool, repo: repository.NewAIAnalysisJobRepository(), alerts: repository.NewAlertRepository(),
		tenantID: testutil.NewTenant(t),
	}
	require.NoError(t, pool.WithTenant(t.Context(), env.tenantID, func(tx pgx.Tx) error {
		return repository.NewLLMProviderRepository().Insert(t.Context(), tx, &domain.LLMProvider{
			TenantID: env.tenantID, Name: "p", Kind: "openai_compatible", Model: "m",
			APIKeySecretRef: "ref", IsDefault: true, AutoAnalyzeAllAlerts: autoOn,
		})
	}))
	return env
}

// newAlertWithJob stores an alert and enqueues its job in one committed
// transaction, the way AlertService.Ingest does.
func (e *jobEnv) newAlertWithJob(t *testing.T) (alertID uuid.UUID, enqueued bool) {
	t.Helper()
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		a := newTestAlert(e.tenantID, domain.SeverityHigh, domain.AlertStatusOpen, nil)
		if err := e.alerts.Insert(t.Context(), tx, a); err != nil {
			return err
		}
		alertID = a.ID
		var err error
		enqueued, err = e.repo.Enqueue(t.Context(), tx, e.tenantID, a.ID, 5)
		return err
	}))
	return alertID, enqueued
}

// jobFor reads the job for an alert through the tenant-scoped path.
func (e *jobEnv) jobFor(t *testing.T, alertID uuid.UUID) *domain.AIAnalysisJob {
	t.Helper()
	var job *domain.AIAnalysisJob
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		var err error
		job, err = e.repo.GetByAlert(t.Context(), tx, alertID)
		return err
	}))
	return job
}

// forceDue makes a job claimable immediately. The tests use the admin
// connection's view of time rather than sleeping.
func forceDue(t *testing.T, e *jobEnv, jobID int64) {
	t.Helper()
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `update ai_analysis_jobs set next_attempt_at = now() - interval '1 second' where id = $1`, jobID)
		return err
	}))
}

func TestAIAnalysisJobRepository_Enqueue(t *testing.T) {
	t.Run("queues a job when the default provider auto-analyses", func(t *testing.T) {
		e := newJobEnv(t, true)
		alertID, enqueued := e.newAlertWithJob(t)
		require.True(t, enqueued)

		job := e.jobFor(t, alertID)
		require.NotNil(t, job)
		assert.Equal(t, domain.AIAnalysisJobPending, job.Status)
		assert.Equal(t, 0, job.Attempts)
		assert.Equal(t, 5, job.MaxAttempts)
		assert.Equal(t, e.tenantID, job.TenantID)
		assert.False(t, job.NextAttemptAt.After(time.Now().Add(time.Second)), "due immediately")
	})

	t.Run("queues nothing when auto-analysis is off", func(t *testing.T) {
		// A tenant that never opted in must not accumulate a row per alert only
		// for each one to be skipped.
		e := newJobEnv(t, false)
		alertID, enqueued := e.newAlertWithJob(t)
		assert.False(t, enqueued)
		assert.Nil(t, e.jobFor(t, alertID))
	})

	t.Run("queues nothing when there is no provider at all", func(t *testing.T) {
		pool := testutil.RequireTestDB(t)
		testutil.ResetAIJobQueue(t)
		e := &jobEnv{pool: pool, repo: repository.NewAIAnalysisJobRepository(), alerts: repository.NewAlertRepository(), tenantID: testutil.NewTenant(t)}
		_, enqueued := e.newAlertWithJob(t)
		assert.False(t, enqueued)
	})

	t.Run("is idempotent per alert", func(t *testing.T) {
		e := newJobEnv(t, true)
		alertID, _ := e.newAlertWithJob(t)
		require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			again, err := e.repo.Enqueue(t.Context(), tx, e.tenantID, alertID, 5)
			assert.False(t, again, "a second enqueue for the same alert must do nothing")
			return err
		}))
	})

	t.Run("commits or rolls back together with the alert", func(t *testing.T) {
		// The reason the job is written in the alert's own transaction: an alert
		// that was not stored must not leave an owed analysis behind, and one
		// that was stored must always have it.
		e := newJobEnv(t, true)
		var alertID uuid.UUID
		err := e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			a := newTestAlert(e.tenantID, domain.SeverityHigh, domain.AlertStatusOpen, nil)
			require.NoError(t, e.alerts.Insert(t.Context(), tx, a))
			alertID = a.ID
			_, err := e.repo.Enqueue(t.Context(), tx, e.tenantID, a.ID, 5)
			require.NoError(t, err)
			return context.Canceled // abort the transaction
		})
		require.Error(t, err)
		assert.Nil(t, e.jobFor(t, alertID), "the job must vanish with the rolled-back alert")
	})

	t.Run("is removed with its alert (data retention deletes alerts)", func(t *testing.T) {
		e := newJobEnv(t, true)
		alertID, _ := e.newAlertWithJob(t)
		require.NotNil(t, e.jobFor(t, alertID))
		require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `delete from alerts where id = $1`, alertID)
			return err
		}))
		assert.Nil(t, e.jobFor(t, alertID), "on delete cascade")
	})
}

func TestAIAnalysisJobRepository_Claim(t *testing.T) {
	t.Run("claims a due job once and counts the attempt", func(t *testing.T) {
		e := newJobEnv(t, true)
		alertID, _ := e.newAlertWithJob(t)

		claimed, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
		require.NoError(t, err)
		mine := filterAlert(claimed, alertID)
		require.Len(t, mine, 1)
		assert.Equal(t, domain.AIAnalysisJobRunning, mine[0].Status)
		assert.Equal(t, 1, mine[0].Attempts, "the attempt is counted at claim time, before any work")
		assert.False(t, mine[0].Reclaimed)
		assert.Equal(t, e.tenantID, mine[0].TenantID, "the consumer learns the tenant from the job itself")

		again, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
		require.NoError(t, err)
		assert.Empty(t, filterAlert(again, alertID), "a running job with a live lease must not be handed out again")
	})

	t.Run("does not claim a job that is not due yet", func(t *testing.T) {
		e := newJobEnv(t, true)
		alertID, _ := e.newAlertWithJob(t)
		claimed, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
		require.NoError(t, err)
		job := filterAlert(claimed, alertID)[0]

		applied, err := e.repo.Reschedule(t.Context(), e.pool, job, time.Hour, "provider down")
		require.NoError(t, err)
		require.True(t, applied)

		later, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
		require.NoError(t, err)
		assert.Empty(t, filterAlert(later, alertID), "backed off for an hour")

		got := e.jobFor(t, alertID)
		assert.Equal(t, domain.AIAnalysisJobPending, got.Status)
		require.NotNil(t, got.LastError)
		assert.Equal(t, "provider down", *got.LastError)
		assert.True(t, got.NextAttemptAt.After(time.Now().Add(50*time.Minute)), "the delay is applied")
		assert.Nil(t, got.LockedUntil)
	})

	t.Run("reclaims a running job whose lease ran out", func(t *testing.T) {
		// The consumer died mid-analysis: nothing will ever finish this job
		// unless the next claim takes it over.
		e := newJobEnv(t, true)
		alertID, _ := e.newAlertWithJob(t)
		first, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
		require.NoError(t, err)
		require.Len(t, filterAlert(first, alertID), 1)

		require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), `update ai_analysis_jobs set locked_until = now() - interval '1 second' where alert_id = $1`, alertID)
			return err
		}))

		second, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
		require.NoError(t, err)
		mine := filterAlert(second, alertID)
		require.Len(t, mine, 1)
		assert.True(t, mine[0].Reclaimed, "the consumer needs to know a half-finished run may be left behind")
		assert.Equal(t, 2, mine[0].Attempts, "a job that keeps killing its consumer must burn through max_attempts")
	})

	t.Run("each job goes to exactly one of several concurrent consumers", func(t *testing.T) {
		// Several ingest replicas work one queue. SKIP LOCKED is what makes
		// "two consumers analyse the same alert" impossible.
		e := newJobEnv(t, true)
		want := map[uuid.UUID]bool{}
		for i := 0; i < 24; i++ {
			id, _ := e.newAlertWithJob(t)
			want[id] = true
		}

		var mu sync.Mutex
		got := map[uuid.UUID]int{}
		var wg sync.WaitGroup
		for c := 0; c < 6; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 8; i++ {
					jobs, err := e.repo.Claim(t.Context(), e.pool, 3, time.Minute)
					if !assert.NoError(t, err) {
						return
					}
					mu.Lock()
					for _, j := range jobs {
						got[j.AlertID]++
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		for id := range want {
			assert.Equal(t, 1, got[id], "alert %s must be claimed exactly once", id)
		}
	})

	t.Run("limit bounds a claim", func(t *testing.T) {
		e := newJobEnv(t, true)
		for i := 0; i < 5; i++ {
			e.newAlertWithJob(t)
		}
		jobs, err := e.repo.Claim(t.Context(), e.pool, 2, time.Minute)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(jobs), 2)
		none, err := e.repo.Claim(t.Context(), e.pool, 0, time.Minute)
		require.NoError(t, err)
		assert.Empty(t, none)
	})
}

// The consumer whose lease lapsed must not be able to write an outcome over the
// new owner's. A guard on status alone could not tell them apart -- the reclaimed
// row is 'running' too -- which is why attempts acts as a fencing token.
func TestAIAnalysisJobRepository_StaleOwnerCannotOverwriteTheNewOne(t *testing.T) {
	e := newJobEnv(t, true)
	alertID, _ := e.newAlertWithJob(t)

	firstClaim, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
	require.NoError(t, err)
	stale := filterAlert(firstClaim, alertID)[0]

	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `update ai_analysis_jobs set locked_until = now() - interval '1 second' where alert_id = $1`, alertID)
		return err
	}))
	secondClaim, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
	require.NoError(t, err)
	current := filterAlert(secondClaim, alertID)[0]
	require.Equal(t, stale.Attempts+1, current.Attempts)

	// The old owner wakes up and tries everything it could.
	applied, err := e.repo.MarkFailed(t.Context(), e.pool, stale, "stale failure")
	require.NoError(t, err)
	assert.False(t, applied, "a stale MarkFailed must be ignored")
	applied, err = e.repo.MarkSucceeded(t.Context(), e.pool, stale)
	require.NoError(t, err)
	assert.False(t, applied)
	applied, err = e.repo.Reschedule(t.Context(), e.pool, stale, time.Hour, "stale")
	require.NoError(t, err)
	assert.False(t, applied)
	applied, err = e.repo.Extend(t.Context(), e.pool, stale, time.Hour)
	require.NoError(t, err)
	assert.False(t, applied, "and must learn it no longer owns the job")

	job := e.jobFor(t, alertID)
	assert.Equal(t, domain.AIAnalysisJobRunning, job.Status, "the current owner's claim is untouched")

	applied, err = e.repo.MarkSucceeded(t.Context(), e.pool, current)
	require.NoError(t, err)
	assert.True(t, applied, "the current owner can finish it")
	assert.Equal(t, domain.AIAnalysisJobSucceeded, e.jobFor(t, alertID).Status)
}

func TestAIAnalysisJobRepository_Outcomes(t *testing.T) {
	claim := func(t *testing.T) (*jobEnv, uuid.UUID, domain.AIAnalysisJob) {
		e := newJobEnv(t, true)
		alertID, _ := e.newAlertWithJob(t)
		jobs, err := e.repo.Claim(t.Context(), e.pool, 100, time.Minute)
		require.NoError(t, err)
		return e, alertID, filterAlert(jobs, alertID)[0]
	}

	t.Run("succeeded", func(t *testing.T) {
		e, alertID, job := claim(t)
		ok, err := e.repo.MarkSucceeded(t.Context(), e.pool, job)
		require.NoError(t, err)
		require.True(t, ok)
		got := e.jobFor(t, alertID)
		assert.Equal(t, domain.AIAnalysisJobSucceeded, got.Status)
		assert.Nil(t, got.LockedUntil)
		assert.Nil(t, got.LastError)
	})

	t.Run("skipped keeps the reason", func(t *testing.T) {
		e, alertID, job := claim(t)
		ok, err := e.repo.MarkSkipped(t.Context(), e.pool, job, "auto-analysis turned off")
		require.NoError(t, err)
		require.True(t, ok)
		got := e.jobFor(t, alertID)
		assert.Equal(t, domain.AIAnalysisJobSkipped, got.Status)
		require.NotNil(t, got.LastError)
		assert.Equal(t, "auto-analysis turned off", *got.LastError)
	})

	t.Run("failed keeps the cause", func(t *testing.T) {
		e, alertID, job := claim(t)
		ok, err := e.repo.MarkFailed(t.Context(), e.pool, job, "the AI provider rejected the credentials (401)")
		require.NoError(t, err)
		require.True(t, ok)
		got := e.jobFor(t, alertID)
		assert.Equal(t, domain.AIAnalysisJobFailed, got.Status)
		require.NotNil(t, got.LastError)
		assert.Contains(t, *got.LastError, "401")
	})

	t.Run("extend moves the lease while the job is owned", func(t *testing.T) {
		e, alertID, job := claim(t)
		before := e.jobFor(t, alertID).LockedUntil
		require.NotNil(t, before)

		ok, err := e.repo.Extend(t.Context(), e.pool, job, time.Hour)
		require.NoError(t, err)
		require.True(t, ok)
		after := e.jobFor(t, alertID).LockedUntil
		require.NotNil(t, after)
		assert.True(t, after.After(before.Add(30*time.Minute)), "the lease was pushed out")
	})

	t.Run("a finished job cannot be extended", func(t *testing.T) {
		e, _, job := claim(t)
		_, err := e.repo.MarkSucceeded(t.Context(), e.pool, job)
		require.NoError(t, err)
		ok, err := e.repo.Extend(t.Context(), e.pool, job, time.Hour)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestAIAnalysisJobRepository_TenantIsolation(t *testing.T) {
	a := newJobEnv(t, true)
	b := newJobEnv(t, true)
	aAlert, _ := a.newAlertWithJob(t)

	t.Run("another tenant's ordinary session cannot see the job", func(t *testing.T) {
		require.NoError(t, b.pool.WithTenant(t.Context(), b.tenantID, func(tx pgx.Tx) error {
			job, err := b.repo.GetByAlert(t.Context(), tx, aAlert)
			assert.Nil(t, job, "tenant_isolation must still apply to every path but the consumer's")
			return err
		}))
	})

	t.Run("a session with no tenant context sees nothing", func(t *testing.T) {
		tx := testutil.BeginTx(t, a.pool, uuid.New()) // some other tenant id
		var n int
		require.NoError(t, tx.QueryRow(t.Context(), `select count(*) from ai_analysis_jobs where alert_id = $1`, aAlert).Scan(&n))
		assert.Zero(t, n)
	})

	t.Run("the consumer, and only through its own opt-in, sees every tenant's jobs", func(t *testing.T) {
		bAlert, _ := b.newAlertWithJob(t)
		claimed, err := a.repo.Claim(t.Context(), a.pool, 1000, time.Minute)
		require.NoError(t, err)
		assert.Len(t, filterAlert(claimed, aAlert), 1)
		assert.Len(t, filterAlert(claimed, bAlert), 1, "one consumer works the queue for all tenants")
	})
}

func TestAIAnalysisJobRepository_PruneAndStats(t *testing.T) {
	e := newJobEnv(t, true)
	oldDone, _ := e.newAlertWithJob(t)
	freshDone, _ := e.newAlertWithJob(t)
	pending, _ := e.newAlertWithJob(t)

	claimed, err := e.repo.Claim(t.Context(), e.pool, 1000, time.Minute)
	require.NoError(t, err)
	for _, id := range []uuid.UUID{oldDone, freshDone} {
		ok, err := e.repo.MarkSucceeded(t.Context(), e.pool, filterAlert(claimed, id)[0])
		require.NoError(t, err)
		require.True(t, ok)
	}
	// Release the third so it counts as pending, overdue.
	ok, err := e.repo.Reschedule(t.Context(), e.pool, filterAlert(claimed, pending)[0], 0, "")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, e.pool.WithTenant(t.Context(), e.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `update ai_analysis_jobs set updated_at = now() - interval '40 days' where alert_id = $1`, oldDone)
		return err
	}))

	st, err := e.repo.Stats(t.Context(), e.pool)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, st.Pending, 1)
	assert.GreaterOrEqual(t, st.Succeeded, 2)

	n, err := e.repo.PruneFinished(t.Context(), e.pool, 30*24*time.Hour)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
	assert.Nil(t, e.jobFor(t, oldDone), "finished long ago: pruned")
	assert.NotNil(t, e.jobFor(t, freshDone), "finished recently: kept for diagnosis")
	assert.NotNil(t, e.jobFor(t, pending), "never prune work that is still owed")
}

func filterAlert(jobs []domain.AIAnalysisJob, alertID uuid.UUID) []domain.AIAnalysisJob {
	var out []domain.AIAnalysisJob
	for _, j := range jobs {
		if j.AlertID == alertID {
			out = append(out, j)
		}
	}
	return out
}
