package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/secrets"
	"github.com/kuruops/kuruops/internal/testutil/cmdtest"
)

// fastTicks runs every sweep every 100ms, so a test sees the real loop dispatch each job.
var fastTicks = tickIntervals{
	refreshViews: 100 * time.Millisecond,
	slaBreaches:  100 * time.Millisecond,
	escalations:  100 * time.Millisecond,
	staleAIRuns:  100 * time.Millisecond,
	retention:    100 * time.Millisecond,
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func workerConfig(t *testing.T, overrides ...string) config.Config {
	t.Helper()
	cfg := cmdtest.CommandConfig(t, overrides...)
	cfg.DatabaseURL = cmdtest.WorkerURL(t, cfg.DatabaseURL)
	return cfg
}

// runWorker starts run() with fast ticks and returns a stop func plus the worker's own
// health/metrics URL once it answers.
func runWorker(t *testing.T, cfg config.Config) (base string, stop func() error) {
	t.Helper()
	return cmdtest.RunCommand(t, cfg, func(ctx context.Context, c config.Config, l *slog.Logger) error {
		return run(ctx, c, l, fastTicks)
	})
}

// TestRunDispatchesEverySweep starts the real worker -- connected as the BYPASSRLS
// kuruops_worker role, exactly as in production -- against a database holding one case for
// each periodic job, and watches the real ticker loop act on all of them: it flags the
// overdue SLA, fails the run a dead process left 'running', and purges the closed alert past
// its retention. Then it checks the per-job gauges the stale-sweep alert depends on and that
// the process stops cleanly. The sweeps' own branches are covered in main_test.go; this is
// the proof that run() schedules and wires them.
func TestRunDispatchesEverySweep(t *testing.T) {
	cfg := workerConfig(t)
	ctx := context.Background()
	admin, err := db.NewPool(ctx, cmdtest.AdminURL(t, cfg), db.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(admin.Close)

	var seedTenant, seedAdmin uuid.UUID
	require.NoError(t, admin.QueryRow(ctx, `select id from tenants order by created_at limit 1`).Scan(&seedTenant))
	require.NoError(t, admin.QueryRow(ctx, `select id from users where email = 'admin@kuruops.local'`).Scan(&seedAdmin))

	overdue := insertSweepTestIncident(t, admin, time.Now().Add(-time.Hour), "detection_analysis")
	staleRun := insertAIRunFixture(t, admin, seedTenant, seedAdmin, "running", time.Now().Add(-2*staleAIRunTimeout))
	retentionTenant := insertSweepTestTenant(t, admin)
	oldAlert := insertSweepTestClosedAlert(t, admin, retentionTenant, time.Now().AddDate(0, -19, 0))

	base, stop := runWorker(t, cfg)

	require.Eventually(t, func() bool { return slaBreachedFor(t, admin, overdue) }, 15*time.Second, 100*time.Millisecond,
		"the SLA sweep never flagged the overdue incident")
	require.Eventually(t, func() bool {
		status, _ := aiRunStatus(t, admin, staleRun)
		return status == "failed"
	}, 15*time.Second, 100*time.Millisecond, "the stale-run sweep never reaped the stuck run")
	require.Eventually(t, func() bool { return !alertExists(t, admin, oldAlert) }, 15*time.Second, 100*time.Millisecond,
		"the retention sweep never purged the closed alert past its window")

	// Every job has now ticked at least once, so each has stamped its success gauge.
	require.Eventually(t, func() bool {
		resp, err := http.Get(base + "/metrics")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body := string(b)
		for _, job := range []string{"refresh_materialized_views", "sweep_sla_breaches", "sweep_escalations", "sweep_stale_ai_runs", "sweep_data_retention"} {
			if !strings.Contains(body, `kuruops_worker_last_sweep_success_timestamp{sweep_job="`+job+`"}`) {
				return false
			}
		}
		return strings.Contains(body, "kuruops_build_info") && strings.Contains(body, "kuruops_db_pool_max_conns")
	}, 15*time.Second, 100*time.Millisecond, "the per-job success gauges were not all exported")

	require.NoError(t, stop(), "run must return nil on a clean shutdown")
}

func TestRunFailsToStartCleanly(t *testing.T) {
	t.Run("database unreachable", func(t *testing.T) {
		cfg := workerConfig(t)
		cfg.DatabaseURL = "postgres://u:p@127.0.0.1:1/kuruops?sslmode=disable&connect_timeout=2"
		err := run(t.Context(), cfg, quietLogger(), fastTicks)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database connection")
	})

	t.Run("an unknown secrets backend", func(t *testing.T) {
		cfg := workerConfig(t, "SECRETS_BACKEND=carrier-pigeon")
		err := run(t.Context(), cfg, quietLogger(), fastTicks)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secrets backend")
	})
}

func TestDefaultTicks(t *testing.T) {
	// The cadence in production: the time-sensitive sweeps every minute, retention (no
	// timeliness requirement, and a table scan) hourly.
	assert.Equal(t, time.Minute, defaultTicks.slaBreaches)
	assert.Equal(t, time.Minute, defaultTicks.escalations)
	assert.Equal(t, time.Minute, defaultTicks.staleAIRuns)
	assert.Equal(t, time.Minute, defaultTicks.refreshViews)
	assert.Equal(t, time.Hour, defaultTicks.retention)
}

// TestSweepsSurviveADatabaseFailure runs every sweep against a pool that is closed -- the
// closest in-process stand-in for "the database went away mid-tick". A sweep that failed
// loudly (panic, os.Exit) would take the whole worker down with it; each must instead log
// the failure and return so the next tick can try again. It is also what exercises the
// failure branches the happy-path tests never reach.
func TestSweepsSurviveADatabaseFailure(t *testing.T) {
	url := cmdtest.AdminURL(t, workerConfig(t))
	pool, err := db.NewPool(context.Background(), url, db.PoolConfig{})
	require.NoError(t, err)
	pool.Close()

	ctx := context.Background()
	logger := quietLogger()
	store := secrets.NewEnvStore()
	escalations, _ := newEscalationPolicyServiceForPool(pool, store)
	smtp := noOnCallSMTP(pool, store)

	assert.NotPanics(t, func() { refreshMaterializedViews(ctx, pool, logger) })
	assert.NotPanics(t, func() { sweepSLABreaches(ctx, pool, logger) })
	assert.NotPanics(t, func() { sweepStaleAIRuns(ctx, pool, logger) })
	assert.NotPanics(t, func() { sweepEscalations(ctx, pool, escalations, smtp, "https://kuruops.example.test", logger) })
	assert.NotPanics(t, func() { sweepDataRetention(ctx, pool, logger) })
	assert.NotPanics(t, func() {
		runLocked(ctx, pool, 999001, "closed-pool-job", logger, func() { t.Error("the job must not run when the lock cannot be taken") })
	})
}
