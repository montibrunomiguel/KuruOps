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
	"github.com/argusops/argusops/internal/secrets"
)

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
	pool, err := db.NewPool(context.Background(), url)
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
	pool, err := db.NewPool(context.Background(), url)
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

		sweepEscalations(ctx, workerPool, store, "https://argusops.example", logger)

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

		sweepEscalations(ctx, workerPool, store, "https://argusops.example", logger)

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

		sweepEscalations(ctx, workerPool, store, "https://argusops.example", logger)

		assert.False(t, notified, "an already-acknowledged alert must not escalate")
	})

	t.Run("a severity with no configured policy is left alone", func(t *testing.T) {
		tenantID := insertSweepTestTenant(t, adminPool)
		alertID := insertSweepTestAlert(t, adminPool, tenantID, "low", "open", time.Now().Add(-24*time.Hour))

		sweepEscalations(ctx, workerPool, store, "https://argusops.example", logger)

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

		sweepEscalations(ctx, workerPool, store, "https://argusops.example", logger)
		sweepEscalations(ctx, workerPool, store, "https://argusops.example", logger)

		assert.Equal(t, 1, callCount, "a second sweep tick must not re-notify an already-escalated alert")
		assert.NotNil(t, escalatedAtFor(t, adminPool, alertID))
	})
}
