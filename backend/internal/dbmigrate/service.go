package dbmigrate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/repository"
)

// Service drives the whole external-database-migration sequence:
// TestConnection -> RunSchemaMigrations -> SetupRoles -> CopyData (see
// Migrate for the orchestrated version). Each step operates directly
// against the customer-supplied target using the privileged credentials
// the admin enters in the Settings panel -- those credentials are never
// persisted, only used for the duration of one Migrate call.
type Service struct {
	// migrationsPath is a filesystem path to db/migrations' .up.sql files,
	// baked into the api image at build time (see backend/Dockerfile) --
	// config.MigrationsPath.
	migrationsPath string
	tenantRepo     *repository.TenantRepository
}

func NewService(migrationsPath string) *Service {
	return &Service{migrationsPath: migrationsPath, tenantRepo: repository.NewTenantRepository()}
}

// TestConnection is the Settings panel's "Test Connection" button: just
// proves the supplied credentials can actually reach and authenticate
// against the target before the admin commits to a real migration.
func (s *Service) TestConnection(ctx context.Context, target TargetConfig) error {
	conn, err := pgx.Connect(ctx, target.DSN())
	if err != nil {
		return fmt.Errorf("connect to target database: %w", err)
	}
	defer conn.Close(ctx)

	if err := conn.Ping(ctx); err != nil {
		return fmt.Errorf("ping target database: %w", err)
	}
	return nil
}

// RunSchemaMigrations replays every db/migrations/*.up.sql against target,
// via golang-migrate as a library (not shelling out to the standalone
// `migrate` CLI/container this repo also uses for local dev -- the api
// process itself needs to be able to do this against an arbitrary
// customer-supplied host, not just the bundled Postgres). Returns the
// resulting schema version.
func (s *Service) RunSchemaMigrations(ctx context.Context, target TargetConfig) (uint, error) {
	sqlDB, err := sql.Open("pgx", target.DSN())
	if err != nil {
		return 0, fmt.Errorf("open target connection: %w", err)
	}
	defer sqlDB.Close()

	driver, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		return 0, fmt.Errorf("build migration driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(toFileURL(s.migrationsPath), "pgx5", driver)
	if err != nil {
		return 0, fmt.Errorf("build migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return 0, fmt.Errorf("apply migrations: %w", err)
	}

	version, _, err := m.Version()
	if err != nil {
		return 0, fmt.Errorf("read resulting schema version: %w", err)
	}
	return version, nil
}

// clearSeedData undoes db/migrations/0002_seed_default_admin.up.sql's
// effect on target: that migration always seeds one 'default' tenant plus
// an admin@argusops.local user the first time it runs against an empty
// `tenants` table, which target's schema replay just triggered -- and
// CopyData is about to copy the *real* source tenant over, which would
// otherwise collide with that seeded row's unique slug. Deleting every row
// from `tenants` (cascades to users and everything else FK'd to it) is
// safe specifically because nothing else in the migration set inserts into
// `tenants`, so this seeded row is always the only one present at this
// point.
func (s *Service) clearSeedData(ctx context.Context, target TargetConfig) error {
	conn, err := pgx.Connect(ctx, target.DSN())
	if err != nil {
		return fmt.Errorf("connect to target database: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `delete from tenants`); err != nil {
		return fmt.Errorf("clear seeded tenant: %w", err)
	}
	return nil
}

// SetupRoles hand-ports db/init/argusops_app_role.sql and
// argusops_worker_role.sql into Go: same statements, same idempotent
// "CREATE ROLE may already exist, ignore that specific error" pattern
// those files document, since ALTER ROLE ... PASSWORD (unlike ordinary
// DML) doesn't accept a bind parameter in Postgres' grammar -- the
// password has to be spliced into the statement text, same as those .sql
// files rely on psql's client-side :'var' substitution to do. This is only
// safe because appPassword/workerPassword are always our own
// crypto/rand-generated, base64url-alphabet tokens (see generatePassword)
// -- never user input -- so there's nothing to escape.
func (s *Service) SetupRoles(ctx context.Context, target TargetConfig, appPassword, workerPassword string) error {
	conn, err := pgx.Connect(ctx, target.DSN())
	if err != nil {
		return fmt.Errorf("connect to target database: %w", err)
	}
	defer conn.Close(ctx)

	if err := createRoleIfNotExists(ctx, conn, "argusops_app",
		"with login nosuperuser nocreatedb nocreaterole nobypassrls"); err != nil {
		return err
	}
	appStatements := []string{
		fmt.Sprintf(`alter role argusops_app with password '%s'`, appPassword),
		`grant usage on schema public to argusops_app`,
		`grant select, insert, update, delete on all tables in schema public to argusops_app`,
		`grant usage, select on all sequences in schema public to argusops_app`,
		`alter default privileges in schema public grant select, insert, update, delete on tables to argusops_app`,
		`alter default privileges in schema public grant usage, select on sequences to argusops_app`,
	}
	for _, stmt := range appStatements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("configure argusops_app: %w", err)
		}
	}

	if err := createRoleIfNotExists(ctx, conn, "argusops_worker",
		"with login nosuperuser nocreatedb nocreaterole bypassrls"); err != nil {
		return err
	}
	workerStatements := []string{
		fmt.Sprintf(`alter role argusops_worker with password '%s'`, workerPassword),
		`grant usage on schema public to argusops_worker`,
		`grant select on all tables in schema public to argusops_worker`,
		`alter default privileges in schema public grant select on tables to argusops_worker`,
		`grant update (sla_breached, updated_at) on incidents to argusops_worker`,
		`grant update (escalated_at) on alerts to argusops_worker`,
		`alter materialized view mv_alert_daily_stats owner to argusops_worker`,
		`alter materialized view mv_incident_kpis owner to argusops_worker`,
		`grant select on mv_alert_daily_stats, mv_incident_kpis to argusops_app`,
	}
	for _, stmt := range workerStatements {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("configure argusops_worker: %w", err)
		}
	}

	return nil
}

func createRoleIfNotExists(ctx context.Context, conn *pgx.Conn, role, opts string) error {
	_, err := conn.Exec(ctx, fmt.Sprintf(`create role %s %s`, role, opts))
	if err != nil && !isDuplicateObject(err) {
		return fmt.Errorf("create role %s: %w", role, err)
	}
	return nil
}

func isDuplicateObject(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42710" // duplicate_object
}

// TableRowCount is CopyData's per-table result: Source and Target must
// match, or the whole migration is rolled back (see CopyData) -- this is
// what backs the "verify with a per-table row-count comparison" step.
type TableRowCount struct {
	Source int64 `json:"source"`
	Target int64 `json:"target"`
}

// CopyData streams every base table from the source (scoped to tenantID --
// see repository.TenantRepository.GetDefault's doc comment on why there's
// only ever one tenant in practice) into target, all inside a single
// target-side transaction for atomicity: if any table's row count doesn't
// match after copying, or any step errors, nothing already written to
// target is kept. Tables are copied in FK-dependency order (parents before
// children, via fkDependencyOrder) -- this schema's foreign keys are
// declared NOT DEFERRABLE (Postgres' default), so a same-transaction
// SET CONSTRAINTS ALL DEFERRED has no effect on them and a child row would
// fail immediately if copied before its parent.
func (s *Service) CopyData(ctx context.Context, sourcePool *db.Pool, tenantID uuid.UUID, target TargetConfig) (map[string]TableRowCount, error) {
	tables, err := listBaseTables(ctx, sourcePool)
	if err != nil {
		return nil, fmt.Errorf("list source tables: %w", err)
	}
	tables, err = fkDependencyOrder(ctx, sourcePool, tables)
	if err != nil {
		return nil, fmt.Errorf("order tables by FK dependency: %w", err)
	}

	targetConn, err := pgx.Connect(ctx, target.DSN())
	if err != nil {
		return nil, fmt.Errorf("connect to target database: %w", err)
	}
	defer targetConn.Close(ctx)

	targetTx, err := targetConn.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin target transaction: %w", err)
	}
	defer targetTx.Rollback(ctx) //nolint:errcheck // no-op once committed

	counts := map[string]TableRowCount{}
	err = sourcePool.WithTenant(ctx, tenantID, func(sourceTx pgx.Tx) error {
		for _, table := range tables {
			targetCount, err := copyTable(ctx, sourceTx, targetTx, table)
			if err != nil {
				return fmt.Errorf("copy %s: %w", table, err)
			}
			var sourceCount int64
			if err := sourceTx.QueryRow(ctx, `select count(*) from `+quoteIdent(table)).Scan(&sourceCount); err != nil {
				return fmt.Errorf("count source rows in %s: %w", table, err)
			}
			counts[table] = TableRowCount{Source: sourceCount, Target: targetCount}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := resetIdentitySequences(ctx, targetTx); err != nil {
		return nil, fmt.Errorf("reset target sequences: %w", err)
	}

	for table, c := range counts {
		if c.Source != c.Target {
			return nil, fmt.Errorf("row count mismatch for %s: copied %d of %d rows", table, c.Target, c.Source)
		}
	}

	if err := targetTx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit target transaction: %w", err)
	}
	return counts, nil
}

// copyTable streams one table's rows from sourceTx to targetTx using raw
// COPY BINARY, without buffering the whole table in memory -- CopyTo and
// CopyFrom are both blocking, so they run on either side of an io.Pipe.
func copyTable(ctx context.Context, sourceTx, targetTx pgx.Tx, table string) (int64, error) {
	pr, pw := io.Pipe()

	var copyToErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, copyToErr = sourceTx.Conn().PgConn().CopyTo(ctx, pw, `copy `+quoteIdent(table)+` to stdout (format binary)`)
		_ = pw.CloseWithError(copyToErr)
	}()

	tag, copyFromErr := targetTx.Conn().PgConn().CopyFrom(ctx, pr, `copy `+quoteIdent(table)+` from stdin (format binary)`)
	<-done

	if copyToErr != nil {
		return 0, fmt.Errorf("read from source: %w", copyToErr)
	}
	if copyFromErr != nil {
		return 0, fmt.Errorf("write to target: %w", copyFromErr)
	}
	return tag.RowsAffected(), nil
}

// listBaseTables excludes golang-migrate's own bookkeeping table --
// schema_migrations already gets the right version row from
// RunSchemaMigrations, copying source's row would just be redundant noise.
// Materialized views (mv_alert_daily_stats/mv_incident_kpis) never appear
// here at all -- information_schema.tables only lists tables and views,
// not matviews (a Postgres-specific deviation from the SQL standard) -- and
// they're derived data cmd/worker will refresh on the target's own
// schedule, not something to copy.
func listBaseTables(ctx context.Context, pool *db.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		select table_name from information_schema.tables
		where table_schema = 'public' and table_type = 'BASE TABLE' and table_name <> 'schema_migrations'
		order by table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// fkDependencyOrder sorts tables so every table appears after every other
// table it has a foreign key into (self-references excluded -- a table's
// own single COPY statement is one statement, and Postgres only checks a
// NOT DEFERRABLE constraint at the end of the statement that violated it,
// so a table referencing itself is fine regardless of row order within
// that one COPY). A plain DFS post-order traversal gives a valid
// topological order; a cycle across two or more different tables would be
// a genuine schema bug (none exist in this schema today), so it's
// surfaced as an error rather than silently guessed at.
func fkDependencyOrder(ctx context.Context, pool *db.Pool, tables []string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		select conrelid::regclass::text as child, confrelid::regclass::text as parent
		from pg_constraint
		where contype = 'f' and connamespace = 'public'::regnamespace and conrelid <> confrelid`)
	if err != nil {
		return nil, fmt.Errorf("query foreign key constraints: %w", err)
	}
	defer rows.Close()

	dependsOn := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		child = strings.TrimPrefix(child, "public.")
		parent = strings.TrimPrefix(parent, "public.")
		dependsOn[child] = append(dependsOn[child], parent)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var order []string
	const unvisited, visiting, done = 0, 1, 2
	state := map[string]int{}
	var visit func(table string) error
	visit = func(table string) error {
		switch state[table] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("circular foreign key dependency involving %s", table)
		}
		state[table] = visiting
		for _, parent := range dependsOn[table] {
			if err := visit(parent); err != nil {
				return err
			}
		}
		state[table] = done
		order = append(order, table)
		return nil
	}

	for _, t := range tables {
		if err := visit(t); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// resetIdentitySequences fixes up every "generated always/by default as
// identity" column's backing sequence after a COPY -- COPY writes the
// explicit id values it was given, it doesn't advance the sequence those
// columns draw new values from, so the very next INSERT after cutover
// would collide with an id COPY already wrote.
func resetIdentitySequences(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `
		select table_name, column_name from information_schema.columns
		where table_schema = 'public' and identity_generation is not null`)
	if err != nil {
		return err
	}
	type idCol struct{ table, column string }
	var cols []idCol
	for rows.Next() {
		var c idCol
		if err := rows.Scan(&c.table, &c.column); err != nil {
			rows.Close()
			return err
		}
		cols = append(cols, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range cols {
		query := fmt.Sprintf(
			`select setval(pg_get_serial_sequence($1, $2), coalesce((select max(%s) from %s), 1), true)`,
			quoteIdent(c.column), quoteIdent(c.table),
		)
		if _, err := tx.Exec(ctx, query, c.table, c.column); err != nil {
			return fmt.Errorf("reset sequence for %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

// toFileURL builds a golang-migrate source URL from a filesystem path. The
// api container only ever passes an absolute Unix path (see
// config.MigrationsPath's default, /app/db/migrations) -- this only needs
// the backslash-to-slash conversion for this package's own tests, which
// run directly on a Windows dev machine where an absolute path looks like
// "C:\foo\bar". No leading slash before the drive letter: golang-migrate's
// source/file driver reconstructs the path as u.Host+u.Path (see its
// parseURL), so "file://C:/foo/bar" parses with host="C:", path="/foo/bar"
// and rejoins to exactly "C:/foo/bar" -- adding a leading slash ourselves
// would instead produce "/C:/foo/bar", which os.DirFS rejects on Windows.
func toFileURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// generatePassword mints a random credential for the target's
// argusops_app/argusops_worker roles -- same crypto/rand +
// base64.RawURLEncoding pattern as webhook token generation
// (service.generateToken), chosen here specifically because that alphabet
// has no quote or backslash characters, which is what makes splicing it
// directly into ALTER ROLE ... PASSWORD '...' (see SetupRoles) safe.
func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// MigrationResult is what the Settings panel shows the admin once Migrate
// succeeds: the schema version now on target, a per-table row-count
// receipt, and the connection strings to put in DATABASE_URL for
// api/ingest (AppDSN, same role) and worker (WorkerDSN, its own
// BYPASSRLS role) before restarting the stack against the new database.
type MigrationResult struct {
	SchemaVersion uint                     `json:"schemaVersion"`
	RowCounts     map[string]TableRowCount `json:"rowCounts"`
	AppDSN        string                   `json:"appDsn"`
	WorkerDSN     string                   `json:"workerDsn"`
}

// Migrate runs the full sequence: test the connection, replay schema
// migrations, stand up the app/worker roles with freshly generated
// passwords, then copy every row over. Nothing here is transactional
// *across* steps -- if CopyData fails after SetupRoles already ran, the
// target is left with roles but no data. That's fine to just retry: every
// step here is naturally idempotent (Up() no-ops once at the latest
// version, SetupRoles tolerates roles that already exist, CopyData is
// fully rolled back on its own failure) -- see each method's own doc
// comment.
func (s *Service) Migrate(ctx context.Context, sourcePool *db.Pool, target TargetConfig) (*MigrationResult, error) {
	tenant, err := s.tenantRepo.GetDefault(ctx, sourcePool)
	if err != nil {
		return nil, fmt.Errorf("resolve default tenant: %w", err)
	}
	if tenant == nil {
		return nil, fmt.Errorf("no tenant configured -- nothing to migrate")
	}

	if err := s.TestConnection(ctx, target); err != nil {
		return nil, fmt.Errorf("test connection: %w", err)
	}

	version, err := s.RunSchemaMigrations(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("run schema migrations: %w", err)
	}

	if err := s.clearSeedData(ctx, target); err != nil {
		return nil, fmt.Errorf("clear seeded default tenant: %w", err)
	}

	appPassword, err := generatePassword()
	if err != nil {
		return nil, fmt.Errorf("generate app role password: %w", err)
	}
	workerPassword, err := generatePassword()
	if err != nil {
		return nil, fmt.Errorf("generate worker role password: %w", err)
	}
	if err := s.SetupRoles(ctx, target, appPassword, workerPassword); err != nil {
		return nil, fmt.Errorf("set up roles: %w", err)
	}

	counts, err := s.CopyData(ctx, sourcePool, tenant.ID, target)
	if err != nil {
		return nil, fmt.Errorf("copy data: %w", err)
	}

	appTarget := target
	appTarget.User, appTarget.Password = "argusops_app", appPassword
	workerTarget := target
	workerTarget.User, workerTarget.Password = "argusops_worker", workerPassword

	return &MigrationResult{
		SchemaVersion: version,
		RowCounts:     counts,
		AppDSN:        appTarget.DSN(),
		WorkerDSN:     workerTarget.DSN(),
	}, nil
}
