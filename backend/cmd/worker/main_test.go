package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// noOnCallDeps builds the on-call/SMTP dependencies sweepEscalations needs,
// bound to pool -- for tests that don't configure a shift or SMTP,
// notifyOnCallAnalyst simply fails its best-effort lookup every time
// (logged, never asserted on), same as it does in production for a tenant
// that hasn't set either up.
func noOnCallDeps(pool *db.Pool) (*service.OnCallShiftService, *repository.UserRepository, *service.SMTPConfigService) {
	users := repository.NewUserRepository()
	onCall := service.NewOnCallShiftService(pool, repository.NewOnCallShiftRepository(), users, repository.NewTenantRepository())
	smtp := service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), secrets.NewEnvStore(), mailer.SMTPSender{})
	return onCall, users, smtp
}

// fakeMailSender is a mailer.Sender test double that records every message
// instead of dialing a real SMTP server.
type fakeMailSender struct {
	sent []mailer.Message
}

func (f *fakeMailSender) Send(_ context.Context, _ mailer.Config, msg mailer.Message) error {
	f.sent = append(f.sent, msg)
	return nil
}

// sweepAdminPool connects with superuser privileges -- used ONLY for
// fixture setup (inserting a throwaway tenant/incident), never for calling
// sweepSLABreaches itself. Same reasoning as testutil.adminPool, not reused
// directly since that helper is unexported.
func sweepAdminPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_ADMIN_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_ADMIN_URL not set -- run via `task backend:test:integration`")
	}
	pool, err := db.NewPool(context.Background(), url, db.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// sweepWorkerPool connects as the real argusops_worker role cmd/worker uses
// in production -- deliberately NOT the superuser admin pool above.
// sweepSLABreaches runs outside Pool.WithTenant by design (see its doc
// comment) and needs its own real grants (SELECT everywhere, a narrow
// UPDATE on incidents -- see db/init/argusops_worker_role.sql) to work at
// all; a superuser connection bypasses every grant, not just RLS, so it
// would silently mask a missing grant exactly like the one this test caught
// during live verification (worker logs: "permission denied for table
// incidents" until argusops_worker_role.sql was given UPDATE on incidents).
func sweepWorkerPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_WORKER_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_WORKER_URL not set -- run via `task backend:test:integration`")
	}
	pool, err := db.NewPool(context.Background(), url, db.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func insertSweepTestIncident(t *testing.T, pool *db.Pool, slaDueAt time.Time, phase string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	tenantID := uuid.New()
	_, err := pool.Exec(ctx, `insert into tenants (id, name, slug) values ($1, $2, $3)`, tenantID, "sweep-"+tenantID.String(), tenantID.String())
	require.NoError(t, err)

	incidentID := uuid.New()
	_, err = pool.Exec(ctx, `
		insert into incidents (id, tenant_id, title, severity, priority, phase, tags, sla_due_at, sla_breached)
		values ($1, $2, 'sweep test incident', 'critical', 'p1', $3, '{}', $4, false)`,
		incidentID, tenantID, phase, slaDueAt,
	)
	require.NoError(t, err)
	return incidentID
}

func slaBreachedFor(t *testing.T, pool *db.Pool, incidentID uuid.UUID) bool {
	t.Helper()
	var breached bool
	err := pool.QueryRow(context.Background(), `select sla_breached from incidents where id = $1`, incidentID).Scan(&breached)
	require.NoError(t, err)
	return breached
}

func TestSweepSLABreaches(t *testing.T) {
	adminPool := sweepAdminPool(t)
	workerPool := sweepWorkerPool(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	t.Run("an overdue incident still in an active phase gets flipped", func(t *testing.T) {
		id := insertSweepTestIncident(t, adminPool, time.Now().Add(-time.Hour), "new")
		sweepSLABreaches(ctx, workerPool, logger)
		assert.True(t, slaBreachedFor(t, adminPool, id))
	})

	t.Run("an incident not yet due is left alone", func(t *testing.T) {
		id := insertSweepTestIncident(t, adminPool, time.Now().Add(time.Hour), "new")
		sweepSLABreaches(ctx, workerPool, logger)
		assert.False(t, slaBreachedFor(t, adminPool, id))
	})

	t.Run("an overdue incident already in post_incident phase is left alone", func(t *testing.T) {
		id := insertSweepTestIncident(t, adminPool, time.Now().Add(-time.Hour), "post_incident")
		sweepSLABreaches(ctx, workerPool, logger)
		assert.False(t, slaBreachedFor(t, adminPool, id))
	})
}

// TestRefreshMaterializedViews inserts a committed alert directly (REFRESH
// MATERIALIZED VIEW CONCURRENTLY only ever sees committed data -- there's no
// tenant-scoped transaction here to commit, admin pool writes are
// auto-committed already) and confirms the refresh actually picks it up,
// proving both that the three views refresh without error under the real
// argusops_worker role/grants and that mv_alert_daily_stats reflects new
// data afterward.
func TestRefreshMaterializedViews(t *testing.T) {
	adminPool := sweepAdminPool(t)
	workerPool := sweepWorkerPool(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	tenantID := insertSweepTestTenant(t, adminPool)
	insertSweepTestAlert(t, adminPool, tenantID, "critical", "open", time.Now())

	refreshMaterializedViews(ctx, workerPool, logger)

	var alertCount int
	err := adminPool.QueryRow(ctx, `select coalesce(sum(alert_count), 0) from mv_alert_daily_stats where tenant_id = $1`, tenantID).Scan(&alertCount)
	require.NoError(t, err)
	assert.Equal(t, 1, alertCount, "the refresh must pick up the alert committed just before it ran")
}

// insertSweepTestTenant is escalation-sweep tests' equivalent of
// insertSweepTestIncident's inline tenant insert -- pulled into its own
// helper since every escalation test needs a fresh tenant plus at least one
// alert and policy row, not just one incident.
func insertSweepTestTenant(t *testing.T, pool *db.Pool) uuid.UUID {
	t.Helper()
	tenantID := uuid.New()
	_, err := pool.Exec(context.Background(), `insert into tenants (id, name, slug) values ($1, $2, $3)`,
		tenantID, "sweep-"+tenantID.String(), tenantID.String())
	require.NoError(t, err)
	return tenantID
}

func insertSweepTestAlert(t *testing.T, pool *db.Pool, tenantID uuid.UUID, severity, status string, receivedAt time.Time) uuid.UUID {
	t.Helper()
	alertID := uuid.New()
	_, err := pool.Exec(context.Background(), `
		insert into alerts (id, tenant_id, title, source, severity, original_severity, status, tags, payload, received_at)
		values ($1, $2, 'sweep test alert', 'test', $3, $3, $4, '{}', '{}', $5)`,
		alertID, tenantID, severity, status, receivedAt,
	)
	require.NoError(t, err)
	return alertID
}

func escalatedAtFor(t *testing.T, pool *db.Pool, alertID uuid.UUID) *time.Time {
	t.Helper()
	var escalatedAt *time.Time
	err := pool.QueryRow(context.Background(), `select escalated_at from alerts where id = $1`, alertID).Scan(&escalatedAt)
	require.NoError(t, err)
	return escalatedAt
}

func TestSweepEscalations(t *testing.T) {
	adminPool := sweepAdminPool(t)
	workerPool := sweepWorkerPool(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()
	store := secrets.NewEnvStore()
	onCall, users, smtp := noOnCallDeps(workerPool)

	t.Run("an overdue open alert with a configured policy fires a notification and stamps escalated_at", func(t *testing.T) {
		var notified bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			notified = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		tenantID := insertSweepTestTenant(t, adminPool)
		ref, err := store.Put(ctx, tenantID.String(), "escalation:high", srv.URL)
		require.NoError(t, err)
		_, err = adminPool.Exec(ctx, `
			insert into escalation_policies (tenant_id, severity, unacknowledged_after_minutes, channel_type, destination_secret_ref)
			values ($1, 'high', 15, 'webhook', $2)`,
			tenantID, ref,
		)
		require.NoError(t, err)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "high", "open", time.Now().Add(-time.Hour))

		sweepEscalations(ctx, workerPool, store, onCall, users, smtp, "https://argusops.example", logger)

		assert.True(t, notified, "the webhook destination must have been called")
		assert.NotNil(t, escalatedAtFor(t, adminPool, alertID))
	})

	t.Run("an alert not yet past the policy's threshold is left alone", func(t *testing.T) {
		var notified bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			notified = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		tenantID := insertSweepTestTenant(t, adminPool)
		ref, err := store.Put(ctx, tenantID.String(), "escalation:high", srv.URL)
		require.NoError(t, err)
		_, err = adminPool.Exec(ctx, `
			insert into escalation_policies (tenant_id, severity, unacknowledged_after_minutes, channel_type, destination_secret_ref)
			values ($1, 'high', 60, 'webhook', $2)`,
			tenantID, ref,
		)
		require.NoError(t, err)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "high", "open", time.Now().Add(-5*time.Minute))

		sweepEscalations(ctx, workerPool, store, onCall, users, smtp, "https://argusops.example", logger)

		assert.False(t, notified)
		assert.Nil(t, escalatedAtFor(t, adminPool, alertID))
	})

	t.Run("an alert already acknowledged (investigating) is left alone even if overdue", func(t *testing.T) {
		var notified bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			notified = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		tenantID := insertSweepTestTenant(t, adminPool)
		ref, err := store.Put(ctx, tenantID.String(), "escalation:critical", srv.URL)
		require.NoError(t, err)
		_, err = adminPool.Exec(ctx, `
			insert into escalation_policies (tenant_id, severity, unacknowledged_after_minutes, channel_type, destination_secret_ref)
			values ($1, 'critical', 15, 'webhook', $2)`,
			tenantID, ref,
		)
		require.NoError(t, err)
		insertSweepTestAlert(t, adminPool, tenantID, "critical", "investigating", time.Now().Add(-time.Hour))

		sweepEscalations(ctx, workerPool, store, onCall, users, smtp, "https://argusops.example", logger)

		assert.False(t, notified, "an already-acknowledged alert must not escalate")
	})

	t.Run("a severity with no configured policy is left alone", func(t *testing.T) {
		tenantID := insertSweepTestTenant(t, adminPool)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "low", "open", time.Now().Add(-24*time.Hour))

		sweepEscalations(ctx, workerPool, store, onCall, users, smtp, "https://argusops.example", logger)

		assert.Nil(t, escalatedAtFor(t, adminPool, alertID))
	})

	t.Run("an already-escalated alert does not fire twice", func(t *testing.T) {
		var callCount int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		tenantID := insertSweepTestTenant(t, adminPool)
		ref, err := store.Put(ctx, tenantID.String(), "escalation:high", srv.URL)
		require.NoError(t, err)
		_, err = adminPool.Exec(ctx, `
			insert into escalation_policies (tenant_id, severity, unacknowledged_after_minutes, channel_type, destination_secret_ref)
			values ($1, 'high', 15, 'webhook', $2)`,
			tenantID, ref,
		)
		require.NoError(t, err)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "high", "open", time.Now().Add(-time.Hour))

		sweepEscalations(ctx, workerPool, store, onCall, users, smtp, "https://argusops.example", logger)
		sweepEscalations(ctx, workerPool, store, onCall, users, smtp, "https://argusops.example", logger)

		assert.Equal(t, 1, callCount, "a second sweep tick must not re-notify an already-escalated alert")
		assert.NotNil(t, escalatedAtFor(t, adminPool, alertID))
	})
}

// TestSweepEscalations_NotifiesOnCallAnalyst confirms the additive on-call
// email step (see notifyOnCallAnalyst): when the tenant has both a shift
// covering right now and SMTP configured, the analyst on that shift gets
// emailed alongside the normal webhook firing -- neither replaces the
// other.
func TestSweepEscalations_NotifiesOnCallAnalyst(t *testing.T) {
	adminPool := sweepAdminPool(t)
	workerPool := sweepWorkerPool(t)
	appPool := testutil.RequireTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()
	store := secrets.NewEnvStore()

	var webhookNotified bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webhookNotified = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tenantID := testutil.NewTenant(t)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	ref, err := store.Put(ctx, tenantID.String(), "escalation:critical", srv.URL)
	require.NoError(t, err)
	_, err = adminPool.Exec(ctx, `
		insert into escalation_policies (tenant_id, severity, unacknowledged_after_minutes, channel_type, destination_secret_ref)
		values ($1, 'critical', 15, 'webhook', $2)`,
		tenantID, ref,
	)
	require.NoError(t, err)
	alertID := insertSweepTestAlert(t, adminPool, tenantID, "critical", "open", time.Now().Add(-time.Hour))

	// On shift every day, all day -- irrelevant of when this test actually
	// runs, "now" always resolves to analystID.
	setupOnCall := service.NewOnCallShiftService(appPool, repository.NewOnCallShiftRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
	for weekday := 0; weekday <= 6; weekday++ {
		_, err := setupOnCall.Create(ctx, tenantID, analystID, weekday, 0, 1439)
		require.NoError(t, err)
	}

	fake := &fakeMailSender{}
	setupSMTP := service.NewSMTPConfigService(appPool, repository.NewSMTPConfigRepository(), store, fake)
	require.NoError(t, setupSMTP.Save(ctx, tenantID, service.SaveSMTPInput{
		Host: "smtp.example.invalid", Port: 587, FromAddress: "argusops@example.invalid",
	}))

	onCall := service.NewOnCallShiftService(workerPool, repository.NewOnCallShiftRepository(), repository.NewUserRepository(), repository.NewTenantRepository())
	users := repository.NewUserRepository()
	smtp := service.NewSMTPConfigService(workerPool, repository.NewSMTPConfigRepository(), store, fake)

	sweepEscalations(ctx, workerPool, store, onCall, users, smtp, "https://argusops.example", logger)

	assert.True(t, webhookNotified, "the configured webhook channel must still fire")
	require.Len(t, fake.sent, 1, "the on-call analyst must also be emailed")
	assert.Contains(t, fake.sent[0].To, "@test.local")
	assert.Contains(t, fake.sent[0].Body, alertID.String())
	assert.NotNil(t, escalatedAtFor(t, adminPool, alertID))
}

// TestRunLocked_OnlyOneReplicaExecutesConcurrently is the regression test
// for the bug this advisory-lock wrapper exists to close: without it, two
// cmd/worker replicas whose tickers fire the same tick would both run
// sweepEscalations' body concurrently, and since it notifies the
// destination BEFORE stamping escalated_at, both would send the same
// escalation. Here two independent pools (standing in for two replicas)
// race to run runLocked for the same key; only the one that gets there
// first must actually execute fn, the other must skip that tick entirely.
func TestRunLocked_OnlyOneReplicaExecutesConcurrently(t *testing.T) {
	replicaA := sweepWorkerPool(t)
	replicaB := sweepWorkerPool(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	key := int64(-990002) // test-only key, distinct from the real job keys

	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	var firstRan, secondRan bool

	go func() {
		defer close(done)
		runLocked(context.Background(), replicaA, key, "test-job", logger, func() {
			firstRan = true
			close(holding)
			<-release
		})
	}()

	<-holding
	runLocked(context.Background(), replicaB, key, "test-job", logger, func() {
		secondRan = true
	})
	close(release)
	<-done

	assert.True(t, firstRan, "the replica that wins the lock must run the job")
	assert.False(t, secondRan, "the replica that loses the lock must skip this tick, not run the job again")
}

// insertAIRunFixture inserts an ai_analysis_runs row with an explicit
// status/updated_at, standing in for a run that got stuck at some point in
// the past (a real stuck run's updated_at is old because nothing ever
// transitioned it again after the goroutine that owned it died -- see
// sweepStaleAIRuns' doc comment).
func insertAIRunFixture(t *testing.T, pool *db.Pool, tenantID, actorID uuid.UUID, status string, updatedAt time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		insert into ai_analysis_runs (tenant_id, context_type, context_id, actor_id, status, updated_at)
		values ($1, 'alert', $2, $3, $4, $5)
		returning id`,
		tenantID, uuid.New(), actorID, status, updatedAt,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func aiRunStatus(t *testing.T, pool *db.Pool, id int64) (status string, errMsg *string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `select status, error from ai_analysis_runs where id = $1`, id).Scan(&status, &errMsg)
	require.NoError(t, err)
	return status, errMsg
}

func TestSweepStaleAIRuns(t *testing.T) {
	adminPool := sweepAdminPool(t)
	workerPool := sweepWorkerPool(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "analyst", nil)

	t.Run("a running run stuck past the timeout is reaped as failed", func(t *testing.T) {
		id := insertAIRunFixture(t, adminPool, tenantID, actorID, "running", time.Now().Add(-2*staleAIRunTimeout))
		sweepStaleAIRuns(context.Background(), workerPool, logger)
		status, errMsg := aiRunStatus(t, adminPool, id)
		assert.Equal(t, "failed", status)
		require.NotNil(t, errMsg)
		assert.Contains(t, *errMsg, "reaped")
	})

	t.Run("a paused run stuck past the timeout is reaped as failed", func(t *testing.T) {
		id := insertAIRunFixture(t, adminPool, tenantID, actorID, "paused", time.Now().Add(-2*staleAIRunTimeout))
		sweepStaleAIRuns(context.Background(), workerPool, logger)
		status, _ := aiRunStatus(t, adminPool, id)
		assert.Equal(t, "failed", status)
	})

	t.Run("a recently-started running run is left alone", func(t *testing.T) {
		id := insertAIRunFixture(t, adminPool, tenantID, actorID, "running", time.Now())
		sweepStaleAIRuns(context.Background(), workerPool, logger)
		status, _ := aiRunStatus(t, adminPool, id)
		assert.Equal(t, "running", status)
	})

	t.Run("a completed run is never touched regardless of age", func(t *testing.T) {
		id := insertAIRunFixture(t, adminPool, tenantID, actorID, "completed", time.Now().Add(-24*time.Hour))
		sweepStaleAIRuns(context.Background(), workerPool, logger)
		status, _ := aiRunStatus(t, adminPool, id)
		assert.Equal(t, "completed", status)
	})

	// A closed pool's Exec always errors, exercising the same "log and
	// return" failure path every other sweep* function in this file takes
	// on a DB error -- without this, that branch has no coverage. A
	// standalone pool (not sweepWorkerPool's, which registers its own
	// t.Cleanup) so closing it here doesn't risk a double-close.
	t.Run("a database error is logged, not panicked on", func(t *testing.T) {
		url := os.Getenv("TEST_DATABASE_WORKER_URL")
		if url == "" {
			t.Skip("TEST_DATABASE_WORKER_URL not set -- run via `task backend:test:integration`")
		}
		brokenPool, err := db.NewPool(context.Background(), url, db.PoolConfig{})
		require.NoError(t, err)
		brokenPool.Close()

		assert.NotPanics(t, func() {
			sweepStaleAIRuns(context.Background(), brokenPool, logger)
		})
	})
}
