package dbmigrate_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/dbmigrate"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

// skipUnlessMigrationTargetEnabled gates this test behind an explicit opt-in
// (run via `task backend:test:migration`) so the normal
// `task backend:test:integration` run doesn't require a second Postgres
// container to be up.
func skipUnlessMigrationTargetEnabled(t *testing.T) dbmigrate.TargetConfig {
	t.Helper()
	if os.Getenv("TEST_MIGRATION_TARGET_ENABLED") == "" {
		t.Skip("TEST_MIGRATION_TARGET_ENABLED not set -- run via `task backend:test:migration`")
	}
	port, err := strconv.Atoi(getenvDefault("TEST_MIGRATION_TARGET_PORT", "5433"))
	require.NoError(t, err)
	return dbmigrate.TargetConfig{
		Host:     getenvDefault("TEST_MIGRATION_TARGET_HOST", "localhost"),
		Port:     port,
		Database: getenvDefault("TEST_MIGRATION_TARGET_DB", "argusops_target"),
		User:     getenvDefault("TEST_MIGRATION_TARGET_USER", "postgres"),
		Password: getenvDefault("TEST_MIGRATION_TARGET_PASSWORD", "postgres"),
	}
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resetTarget wipes postgres-target back to an empty database -- it's
// disposable test infrastructure, reset on every run rather than requiring
// a fresh container each time.
func resetTarget(t *testing.T, target dbmigrate.TargetConfig) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), target.DSN())
	require.NoError(t, err)
	defer conn.Close(t.Context())

	_, err = conn.Exec(t.Context(), `
		drop schema public cascade;
		create schema public;
		grant all on schema public to public;
		drop role if exists argusops_app;
		drop role if exists argusops_worker;`)
	require.NoError(t, err)
}

// migrationsDirForTest resolves db/migrations relative to this test file's
// own location (repo root's db/migrations, three levels up from
// backend/internal/dbmigrate) rather than assuming a working directory --
// `go test` always runs with cwd set to the package under test.
func migrationsDirForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "db", "migrations"))
	require.NoError(t, err)
	return abs
}

func TestService_Migrate_EndToEnd(t *testing.T) {
	target := skipUnlessMigrationTargetEnabled(t)
	resetTarget(t, target)

	pool := testutil.RequireTestDB(t)

	// Migrate() resolves "the" tenant the same way production login does --
	// TenantRepository.GetDefault, the oldest row in `tenants` -- rather
	// than taking a tenant ID as an argument (this is single-instance
	// software; see that method's doc comment). A fresh argusops_test
	// already has exactly one tenant at this point: the one
	// db/migrations/0013_seed_default_admin.up.sql seeds automatically the
	// first time migrations run against an empty database. Seed fixtures
	// under that same tenant, not a new one via testutil.NewTenant, or
	// Migrate would migrate a tenant this test never populated.
	tenantRepo := repository.NewTenantRepository()
	defaultTenant, err := tenantRepo.GetDefault(t.Context(), pool)
	require.NoError(t, err)
	require.NotNil(t, defaultTenant, "argusops_test must have the seeded default tenant -- run via `task backend:test:migration`, which depends on db:test:reset")
	tenantID := defaultTenant.ID

	alertRepo := repository.NewAlertRepository()
	incidentRepo := repository.NewIncidentRepository()

	// Seed data committed outside testutil.BeginTx's auto-rollback -- Migrate
	// reads through its own fresh connections, so anything left uncommitted
	// here would be invisible to it.
	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), "select set_config('app.tenant_id', $1, true)", tenantID.String())
	require.NoError(t, err)

	a := &domain.Alert{
		TenantID: tenantID, Title: "Suspicious login", Source: "test",
		Severity: domain.SeverityHigh, OriginalSeverity: domain.SeverityHigh, Status: domain.AlertStatusOpen,
		Tags: []string{}, Payload: json.RawMessage(`{}`), ReceivedAt: time.Now(),
	}
	require.NoError(t, alertRepo.Insert(t.Context(), tx, a))
	require.NoError(t, alertRepo.InsertEvent(t.Context(), tx, &domain.AlertEvent{
		AlertID: a.ID, TenantID: tenantID, EventType: domain.AlertEventReceived,
		ActorType: domain.ActorSystem, Data: json.RawMessage(`{}`),
	}))

	inc := &domain.Incident{
		TenantID: tenantID, Title: "Ransomware suspected", Severity: domain.SeverityCritical,
		Priority: domain.PriorityP1, Phase: domain.PhaseNew, Tags: []string{},
	}
	require.NoError(t, incidentRepo.Insert(t.Context(), tx, inc))
	require.NoError(t, tx.Commit(t.Context()))

	svc := dbmigrate.NewService(migrationsDirForTest(t))
	result, err := svc.Migrate(t.Context(), pool, target)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, result.SchemaVersion, uint(0))

	alertsCount, ok := result.RowCounts["alerts"]
	require.True(t, ok, "alerts must be one of the copied tables")
	assert.Equal(t, int64(1), alertsCount.Source)
	assert.Equal(t, alertsCount.Source, alertsCount.Target)

	incidentsCount, ok := result.RowCounts["incidents"]
	require.True(t, ok)
	assert.Equal(t, int64(1), incidentsCount.Source)
	assert.Equal(t, incidentsCount.Source, incidentsCount.Target)

	appConn, err := pgx.Connect(t.Context(), result.AppDSN)
	require.NoError(t, err)
	defer appConn.Close(t.Context())

	var countNoTenant int
	require.NoError(t, appConn.QueryRow(t.Context(), "select count(*) from alerts").Scan(&countNoTenant))
	assert.Zero(t, countNoTenant, "RLS must hide rows on the target too when app.tenant_id isn't set")

	_, err = appConn.Exec(t.Context(), "select set_config('app.tenant_id', $1, false)", tenantID.String())
	require.NoError(t, err)

	var countWithTenant int
	require.NoError(t, appConn.QueryRow(t.Context(), "select count(*) from alerts").Scan(&countWithTenant))
	assert.Equal(t, 1, countWithTenant)

	var newEventID int64
	err = appConn.QueryRow(t.Context(), `
		insert into alert_events (alert_id, tenant_id, event_type, actor_type, data)
		values ($1, $2, 'status_changed', 'system', '{}') returning id`, a.ID, tenantID,
	).Scan(&newEventID)
	require.NoError(t, err, "the alert_events identity sequence must be advanced past copied rows, or this collides")

	var mvOwner string
	require.NoError(t, appConn.QueryRow(t.Context(),
		`select matviewowner from pg_matviews where matviewname = 'mv_alert_daily_stats'`).Scan(&mvOwner))
	assert.Equal(t, "argusops_worker", mvOwner)
}

func TestService_TestConnection(t *testing.T) {
	target := skipUnlessMigrationTargetEnabled(t)
	svc := dbmigrate.NewService(migrationsDirForTest(t))

	t.Run("reachable target succeeds", func(t *testing.T) {
		assert.NoError(t, svc.TestConnection(t.Context(), target))
	})

	t.Run("wrong password fails", func(t *testing.T) {
		bad := target
		bad.Password = "definitely-wrong"
		assert.Error(t, svc.TestConnection(t.Context(), bad))
	})

	t.Run("unreachable host fails", func(t *testing.T) {
		bad := target
		bad.Host = "no-such-host.invalid"
		assert.Error(t, svc.TestConnection(t.Context(), bad))
	})
}
