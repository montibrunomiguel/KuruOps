package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver"
	"github.com/argusops/argusops/internal/mailer"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// newEscalationPolicyServiceForPool builds an EscalationPolicyService bound
// to pool and store -- used both on the setup side (an RLS-scoped app pool,
// via EscalationPolicyService.Save/OnCallScheduleService.Create) and on the
// sweep side (workerPool, matching what cmd/worker actually wires up). store
// must be the same instance across both sides for a secret Put on the setup
// side to Resolve correctly on the sweep side.
func newEscalationPolicyServiceForPool(pool *db.Pool, store secrets.Store) (*service.EscalationPolicyService, *service.OnCallScheduleService) {
	users := repository.NewUserRepository()
	userSvc := service.NewUserService(pool, users)
	scheduleRepo := repository.NewOnCallScheduleRepository()
	onCall := service.NewOnCallScheduleService(pool, scheduleRepo, users, repository.NewTenantRepository())
	escalationPolicies := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), scheduleRepo, onCall, userSvc, store)
	return escalationPolicies, onCall
}

// sweepTestStep is one step of a newSweepTestChain-built chain: its own
// httptest server plus a hit counter, so a test can assert exactly which
// step(s) fired and how many times.
type sweepTestStep struct {
	server *httptest.Server
	hits   *int32
}

func (s *sweepTestStep) Hits() int32 { return atomic.LoadInt32(s.hits) }

// newSweepTestChain creates an all-day, always-on-call schedule for tenantID
// (optionally covering participantIDs) via appPool's RLS-scoped services,
// then saves a severity's escalation chain with the given per-step delays,
// each step firing to its own httptest server -- returns the steps in order
// so callers can assert which one(s) fired.
func newSweepTestChain(t *testing.T, appPool *db.Pool, store secrets.Store, tenantID uuid.UUID, severity domain.Severity, participantIDs []uuid.UUID, delayMinutes ...int) []*sweepTestStep {
	t.Helper()
	ctx := context.Background()

	escalationPolicies, onCall := newEscalationPolicyServiceForPool(appPool, store)
	sched, err := onCall.Create(ctx, tenantID, domain.SaveOnCallScheduleInput{
		Name: "Primary", ParticipantIDs: participantIDs, HandoverAt: time.Now().Add(-24 * time.Hour),
		PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	})
	require.NoError(t, err)

	steps := make([]*sweepTestStep, len(delayMinutes))
	saveSteps := make([]domain.SaveEscalationStepInput, len(delayMinutes))
	for i, delay := range delayMinutes {
		hits := new(int32)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(hits, 1)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		steps[i] = &sweepTestStep{server: srv, hits: hits}
		saveSteps[i] = domain.SaveEscalationStepInput{ScheduleID: sched.ID, DelayMinutes: delay, ChannelType: domain.EscalationChannelWebhook, Destination: srv.URL}
	}
	_, err = escalationPolicies.Save(ctx, tenantID, domain.SaveEscalationPolicyInput{Severity: severity, Steps: saveSteps})
	require.NoError(t, err)
	return steps
}

func slaEscalationStepFor(t *testing.T, pool *db.Pool, alertID uuid.UUID) int {
	t.Helper()
	var step int
	err := pool.QueryRow(context.Background(), `select sla_escalation_step from alerts where id = $1`, alertID).Scan(&step)
	require.NoError(t, err)
	return step
}

func setEscalationProgress(t *testing.T, pool *db.Pool, alertID uuid.UUID, slaStep int, escalatedAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `update alerts set sla_escalation_step = $2, escalated_at = $3 where id = $1`, alertID, slaStep, escalatedAt)
	require.NoError(t, err)
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
// alert and policy row, not just one incident. Registers cleanupTenant --
// unlike most fixtures in this codebase, an escalation-sweep tenant's alert
// deliberately has NO terminal "already escalated" state to age out of the
// sweep's candidate query (that's the whole point of the automatic loop),
// so an uncleaned tenant would stay a live candidate for every future sweep
// call for the rest of this test binary's run (and, since Postgres test
// data isn't reset between `task backend:test:integration` invocations,
// every run after this one too) -- each of those tries to resolve a secret
// ref through a `secrets.EnvStore` instance that only ever lived in this
// one process, silently resolving to "" and failing every retry attempt.
func insertSweepTestTenant(t *testing.T, pool *db.Pool) uuid.UUID {
	t.Helper()
	tenantID := uuid.New()
	_, err := pool.Exec(context.Background(), `insert into tenants (id, name, slug) values ($1, $2, $3)`,
		tenantID, "sweep-"+tenantID.String(), tenantID.String())
	require.NoError(t, err)
	cleanupTenant(t, pool, tenantID)
	return tenantID
}

// cleanupTenant deletes tenantID (cascading to every row that references
// it -- alerts, escalation_policies/_steps, on_call_schedules, etc, see
// their tenant_id foreign keys' `on delete cascade`) once the current test
// finishes. See insertSweepTestTenant's doc comment for why escalation-sweep
// tests in particular need this where most fixtures elsewhere don't bother.
func cleanupTenant(t *testing.T, pool *db.Pool, tenantID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `delete from tenants where id = $1`, tenantID)
	})
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
	appPool := testutil.RequireTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()

	t.Run("an overdue open alert with a configured chain fires its first step and advances sla_escalation_step", func(t *testing.T) {
		store := secrets.NewEnvStore()
		tenantID := testutil.NewTenant(t)
		cleanupTenant(t, adminPool, tenantID)
		steps := newSweepTestChain(t, appPool, store, tenantID, domain.SeverityHigh, nil, 15)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "high", "open", time.Now().Add(-time.Hour))

		escalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)

		assert.Equal(t, int32(1), steps[0].Hits(), "step 0's destination must have been called exactly once")
		assert.NotNil(t, escalatedAtFor(t, adminPool, alertID))
		assert.Equal(t, 1, slaEscalationStepFor(t, adminPool, alertID))
	})

	t.Run("an alert not yet past the first step's delay is left alone", func(t *testing.T) {
		store := secrets.NewEnvStore()
		tenantID := testutil.NewTenant(t)
		cleanupTenant(t, adminPool, tenantID)
		steps := newSweepTestChain(t, appPool, store, tenantID, domain.SeverityHigh, nil, 60)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "high", "open", time.Now().Add(-5*time.Minute))

		escalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)

		assert.Equal(t, int32(0), steps[0].Hits())
		assert.Nil(t, escalatedAtFor(t, adminPool, alertID))
		assert.Equal(t, 0, slaEscalationStepFor(t, adminPool, alertID))
	})

	t.Run("an already-acknowledged (investigating) alert still escalates -- the loop runs until attended, not just until acknowledged", func(t *testing.T) {
		store := secrets.NewEnvStore()
		tenantID := testutil.NewTenant(t)
		cleanupTenant(t, adminPool, tenantID)
		steps := newSweepTestChain(t, appPool, store, tenantID, domain.SeverityCritical, nil, 15)
		insertSweepTestAlert(t, adminPool, tenantID, "critical", "investigating", time.Now().Add(-time.Hour))

		escalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)

		assert.Equal(t, int32(1), steps[0].Hits(), "an alert stuck in investigating must keep escalating, per \"roda as escalas até ser atendido\"")
	})

	t.Run("a severity with no configured chain is left alone", func(t *testing.T) {
		store := secrets.NewEnvStore()
		tenantID := testutil.NewTenant(t)
		cleanupTenant(t, adminPool, tenantID)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "low", "open", time.Now().Add(-24*time.Hour))

		escalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)

		assert.Nil(t, escalatedAtFor(t, adminPool, alertID))
	})

	t.Run("a second tick before the next step is due does not re-fire", func(t *testing.T) {
		store := secrets.NewEnvStore()
		tenantID := testutil.NewTenant(t)
		cleanupTenant(t, adminPool, tenantID)
		steps := newSweepTestChain(t, appPool, store, tenantID, domain.SeverityHigh, nil, 15)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "high", "open", time.Now().Add(-time.Hour))

		escalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)

		assert.Equal(t, int32(1), steps[0].Hits(), "a second sweep tick must not re-fire a step whose own delay hasn't elapsed since the last fire")
		assert.NotNil(t, escalatedAtFor(t, adminPool, alertID))
	})

	t.Run("a 2-step chain advances to step 1 once step 0's delay has passed since the last fire, then wraps back to step 0", func(t *testing.T) {
		store := secrets.NewEnvStore()
		tenantID := testutil.NewTenant(t)
		cleanupTenant(t, adminPool, tenantID)
		steps := newSweepTestChain(t, appPool, store, tenantID, domain.SeverityHigh, nil, 15, 30)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "high", "open", time.Now().Add(-2*time.Hour))
		escalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)

		// Simulate step 0 having already fired 40 minutes ago -- step 1's own
		// 30-minute delay (since the last fire) has now elapsed, so this tick
		// must fire step 1, not step 0 again.
		setEscalationProgress(t, adminPool, alertID, 1, time.Now().Add(-40*time.Minute))
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)

		assert.Equal(t, int32(0), steps[0].Hits(), "step 0 must not re-fire")
		assert.Equal(t, int32(1), steps[1].Hits(), "step 1 must fire")
		assert.Equal(t, 2, slaEscalationStepFor(t, adminPool, alertID))

		// Simulate step 1 having fired 40 minutes ago too -- the chain has
		// only 2 steps, so `2 % 2 == 0` must wrap back around to step 0.
		setEscalationProgress(t, adminPool, alertID, 2, time.Now().Add(-40*time.Minute))
		sweepEscalations(ctx, workerPool, escalationPolicies, noOnCallSMTP(workerPool, store), "https://argusops.example", logger)

		assert.Equal(t, int32(1), steps[0].Hits(), "step 0 must fire again -- this is the wraparound, \"roda as escalas até ser atendido\"")
		assert.Equal(t, 3, slaEscalationStepFor(t, adminPool, alertID))
	})
}

// noOnCallSMTP builds the SMTPConfigService sweepEscalations needs, bound to
// pool/store -- for tests that don't configure SMTP, the best-effort
// on-call email step simply fails its lookup every time (logged, never
// asserted on), same as it does in production for a tenant that hasn't set
// SMTP up.
func noOnCallSMTP(pool *db.Pool, store secrets.Store) *service.SMTPConfigService {
	return service.NewSMTPConfigService(pool, repository.NewSMTPConfigRepository(), store, mailer.SMTPSender{})
}

// TestSweepEscalations_NotifiesOnCallAnalyst confirms the additive on-call
// email step (see emailOnCallAnalyst): when the tenant has both a shift
// covering right now (on the step's own schedule) and SMTP configured, the
// analyst on that shift gets emailed alongside the normal webhook firing --
// neither replaces the other.
func TestSweepEscalations_NotifiesOnCallAnalyst(t *testing.T) {
	adminPool := sweepAdminPool(t)
	workerPool := sweepWorkerPool(t)
	appPool := testutil.RequireTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()
	store := secrets.NewEnvStore()

	tenantID := testutil.NewTenant(t)
	cleanupTenant(t, adminPool, tenantID)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	// On shift every day, all day -- irrelevant of when this test actually
	// runs, "now" always resolves to analystID.
	steps := newSweepTestChain(t, appPool, store, tenantID, domain.SeverityCritical, []uuid.UUID{analystID}, 15)
	alertID := insertSweepTestAlert(t, adminPool, tenantID, "critical", "open", time.Now().Add(-time.Hour))

	fake := &fakeMailSender{}
	setupSMTP := service.NewSMTPConfigService(appPool, repository.NewSMTPConfigRepository(), store, fake)
	require.NoError(t, setupSMTP.Save(ctx, tenantID, service.SaveSMTPInput{
		Host: "smtp.example.invalid", Port: 587, FromAddress: "argusops@example.invalid",
	}))

	escalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)
	smtp := service.NewSMTPConfigService(workerPool, repository.NewSMTPConfigRepository(), store, fake)

	sweepEscalations(ctx, workerPool, escalationPolicies, smtp, "https://argusops.example", logger)

	assert.Equal(t, int32(1), steps[0].Hits(), "the configured webhook channel must still fire")
	require.Len(t, fake.sent, 1, "the on-call analyst must also be emailed")
	assert.Contains(t, fake.sent[0].To, "@test.local")
	assert.Contains(t, fake.sent[0].Body, alertID.String())
	assert.NotNil(t, escalatedAtFor(t, adminPool, alertID))
}

// TestSweepEscalations_FailedSendDoesNotStampOrEmailOnCall is the
// regression test for reordering escalated_at's stamp to happen right after
// a successful Send instead of after the best-effort on-call email step:
// the stamp must still be gated on Send actually succeeding, not
// unconditional -- a destination that's down (retried by
// notifier.RetryingSender and still failing every attempt) must leave the
// alert's sla_escalation_step/escalated_at untouched so the next sweep tick
// retries the same step, and must never reach the on-call email step either.
func TestSweepEscalations_FailedSendDoesNotStampOrEmailOnCall(t *testing.T) {
	adminPool := sweepAdminPool(t)
	workerPool := sweepWorkerPool(t)
	appPool := testutil.RequireTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx := context.Background()
	store := secrets.NewEnvStore()

	tenantID := testutil.NewTenant(t)
	cleanupTenant(t, adminPool, tenantID)
	analystID := testutil.NewUser(t, tenantID, "analyst", nil)

	// A chain whose sole step points at a destination that always 500s.
	escalationPolicies, onCall := newEscalationPolicyServiceForPool(appPool, store)
	sched, err := onCall.Create(ctx, tenantID, domain.SaveOnCallScheduleInput{
		Name: "Primary", ParticipantIDs: []uuid.UUID{analystID}, HandoverAt: time.Now().Add(-24 * time.Hour),
		PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	})
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err = escalationPolicies.Save(ctx, tenantID, domain.SaveEscalationPolicyInput{
		Severity: domain.SeverityCritical,
		Steps:    []domain.SaveEscalationStepInput{{ScheduleID: sched.ID, DelayMinutes: 15, ChannelType: domain.EscalationChannelWebhook, Destination: srv.URL}},
	})
	require.NoError(t, err)
	alertID := insertSweepTestAlert(t, adminPool, tenantID, "critical", "open", time.Now().Add(-time.Hour))

	fake := &fakeMailSender{}
	setupSMTP := service.NewSMTPConfigService(appPool, repository.NewSMTPConfigRepository(), store, fake)
	require.NoError(t, setupSMTP.Save(ctx, tenantID, service.SaveSMTPInput{
		Host: "smtp.example.invalid", Port: 587, FromAddress: "argusops@example.invalid",
	}))

	workerEscalationPolicies, _ := newEscalationPolicyServiceForPool(workerPool, store)
	smtp := service.NewSMTPConfigService(workerPool, repository.NewSMTPConfigRepository(), store, fake)

	sweepEscalations(ctx, workerPool, workerEscalationPolicies, smtp, "https://argusops.example", logger)

	assert.Nil(t, escalatedAtFor(t, adminPool, alertID), "a failed Send must leave the alert un-escalated so the next tick retries it")
	assert.Equal(t, 0, slaEscalationStepFor(t, adminPool, alertID))
	assert.Empty(t, fake.sent, "the on-call email step must never run when the primary Send failed")
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

// TestRunLocked_RecordsSweepSuccessMetric is the regression test for the
// argusops_worker_last_sweep_success_timestamp gauge (see
// MetricsCollector.RecordSweepSuccess): it must be stamped when a tick
// actually acquires the lock and runs fn, and must NOT be stamped for a
// tick that loses the lock and skips -- an Alertmanager rule watching this
// gauge for staleness would otherwise never fire on a genuinely stuck job
// (if a losing replica stamped it anyway) or false-alarm on a healthy
// multi-replica setup (if a winning replica's tick didn't stamp it at all).
func TestRunLocked_RecordsSweepSuccessMetric(t *testing.T) {
	pool := sweepWorkerPool(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	m := &httpserver.MetricsCollector{}
	orig := httpserver.SetMetricsForTest(m)
	defer httpserver.SetMetricsForTest(orig)

	runLocked(context.Background(), pool, -990003, "test-metric-job", logger, func() {})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	httpserver.MetricsHandler(rec, req)

	assert.Contains(t, rec.Body.String(), `argusops_worker_last_sweep_success_timestamp{sweep_job="test-metric-job"}`)
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
