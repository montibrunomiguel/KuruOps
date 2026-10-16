// Package cmdtest helps a cmd/* test start the real command -- run(ctx, cfg, logger) --
// against a database of its own and talk to it over HTTP.
//
// It is a separate package from testutil because it needs internal/dbmigrate (to build the
// schema in a fresh database), and dbmigrate's own tests import testutil: putting this in
// testutil would be an import cycle.
package cmdtest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/config"
	"github.com/kuruops/kuruops/internal/dbmigrate"
)

// FreeAddr returns a loopback host:port that was free a moment ago -- for a command's
// HTTP_ADDR in a test that starts the real server.
func FreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

// FreshDatabase creates an empty database on the test server, builds the full schema in it
// (every migration, including the seeded default admin) and grants the application role what
// production grants it, then returns the connection URL of that role. The database is
// dropped when the test ends. Skips the test without TEST_DATABASE_ADMIN_URL.
//
// Why a database of its own rather than the shared test database: a command boots the
// WHOLE system, including the secret store, which decrypts every stored secret at startup
// and refuses to start if one was encrypted under another key -- and the shared test
// database is full of rows other tests wrote under their own keys. A fresh database is
// also the honest test of "does this start on an empty install".
//
// It deliberately does not call dbmigrate.Service.SetupRoles: that resets the role
// passwords, and the roles are cluster-wide, so it would break every other database on the
// server (and any real stack sharing it).
func FreshDatabase(t *testing.T) (appURL string) {
	t.Helper()
	adminURL := os.Getenv("TEST_DATABASE_ADMIN_URL")
	appBase := os.Getenv("TEST_DATABASE_URL")
	if adminURL == "" || appBase == "" {
		t.Skip("TEST_DATABASE_ADMIN_URL / TEST_DATABASE_URL not set -- run via `task backend:test:integration`")
	}
	admin, err := url.Parse(adminURL)
	require.NoError(t, err)
	app, err := url.Parse(appBase)
	require.NoError(t, err)

	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	name := "kuruops_cmd_" + hex.EncodeToString(suffix)

	ctx := context.Background()
	rootURL := *admin
	rootURL.Path = "/postgres"
	root, err := pgx.Connect(ctx, rootURL.String())
	require.NoError(t, err)
	_, err = root.Exec(ctx, "create database "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = root.Exec(context.Background(), "drop database if exists "+name+" with (force)")
		_ = root.Close(context.Background())
	})

	port := 5432
	if p := admin.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	password, _ := admin.User.Password()
	target := dbmigrate.TargetConfig{Host: admin.Hostname(), Port: port, Database: name, User: admin.User.Username(), Password: password}
	_, err = dbmigrate.NewService(migrationsDir(t)).RunSchemaMigrations(ctx, target)
	require.NoError(t, err, "build the schema in the fresh database")

	conn, err := pgx.Connect(ctx, target.DSN())
	require.NoError(t, err)
	defer conn.Close(ctx)
	// The roles' grants come from the very scripts production runs (db/init), so these tests
	// cannot drift from them: if the worker gains a table to write, the script changes and so
	// does the schema these commands boot on.
	for _, script := range []string{"kuruops_app_role.sql", "kuruops_worker_role.sql"} {
		for _, stmt := range grantStatements(t, filepath.Join(initDir(t), script)) {
			_, err := conn.Exec(ctx, stmt)
			require.NoError(t, err, "%s: %s", script, stmt)
		}
	}

	out := *app
	out.Path = "/" + name
	return out.String()
}

// WorkerURL turns the fresh database's application URL into the one for the worker role
// (BYPASSRLS), with the credentials from TEST_DATABASE_WORKER_URL.
func WorkerURL(t *testing.T, appURL string) string {
	t.Helper()
	worker, err := url.Parse(os.Getenv("TEST_DATABASE_WORKER_URL"))
	if err != nil || os.Getenv("TEST_DATABASE_WORKER_URL") == "" {
		t.Skip("TEST_DATABASE_WORKER_URL not set -- run via `task backend:test:integration`")
	}
	app, err := url.Parse(appURL)
	require.NoError(t, err)
	worker.Path = app.Path
	return worker.String()
}

// grantStatements returns the executable statements of a db/init role script -- parsed by
// the same dbmigrate.ParseRoleScript production uses -- minus the two kinds that create the
// role or set its password: the roles already exist cluster-wide with known passwords, and
// changing a password here would break every other database on the server.
func grantStatements(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	// The passwords never reach the server: the statements that would use them are dropped below.
	all, err := dbmigrate.ParseRoleScript(string(raw), map[string]string{"app_password": "x", "worker_password": "x"})
	require.NoError(t, err)
	var stmts []string
	for _, stmt := range all {
		lower := strings.ToLower(strings.TrimSpace(stmt))
		if strings.HasPrefix(lower, "create role") || strings.HasPrefix(lower, "alter role") {
			continue
		}
		stmts = append(stmts, stmt)
	}
	return stmts
}

func initDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(migrationsDir(t)), "init")
}

// migrationsDir finds db/migrations by walking up from the test's working directory.
func migrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		candidate := filepath.Join(dir, "db", "migrations")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "db/migrations not found above %s", dir)
		dir = parent
	}
}

// CommandConfig builds the config a cmd/* test starts the real command with: a fresh
// database (as the least-privilege application role, like production), the dev auth mode
// (so no JWT key files are needed), a loopback HTTP address, and short timeouts. It reads the
// same environment the command does, through config.Load, so what the test runs is what the
// command would run. overrides are KEY=VALUE pairs applied on top.
func CommandConfig(t *testing.T, overrides ...string) config.Config {
	t.Helper()
	t.Setenv("DATABASE_URL", FreshDatabase(t))
	t.Setenv("AUTH_MODE", "dev")
	t.Setenv("HTTP_ADDR", FreeAddr(t))
	t.Setenv("DEV_KEYS_DIR", t.TempDir())
	t.Setenv("UPLOAD_DIR", t.TempDir())
	t.Setenv("SHUTDOWN_TIMEOUT", "10s")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	// Any 32 bytes will do: the secret store encrypts with it, and nothing here is real.
	t.Setenv("SECRETS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	for _, kv := range overrides {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				t.Setenv(kv[:i], kv[i+1:])
				break
			}
		}
	}
	cfg, err := config.Load(slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	return cfg
}

// RunCommand starts run(ctx, cfg) in the background and returns the base URL of its HTTP
// server once /healthz answers 200, plus a stop func that cancels it and returns what run
// returned. It fails the test if the command stops early or never becomes healthy.
func RunCommand(t *testing.T, cfg config.Config, run func(context.Context, config.Config, *slog.Logger) error) (baseURL string, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	baseURL = "http://" + cfg.HTTPAddr
	deadline := time.Now().Add(20 * time.Second)
	for healthy := false; !healthy; {
		select {
		case err := <-done:
			cancel()
			t.Fatalf("command stopped before becoming healthy: %v", err)
		default:
		}
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			healthy = resp.StatusCode == http.StatusOK
		}
		if !healthy {
			if time.Now().After(deadline) {
				cancel()
				t.Fatalf("command did not become healthy within 20s (last error: %v)", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	var stopped bool
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			return fmt.Errorf("command did not stop within 30s of cancellation")
		}
	}
	t.Cleanup(func() { _ = stop() })
	return baseURL, stop
}

// AdminConn opens a superuser connection to the database cfg points at (the fresh one
// CommandConfig created), for setup and assertions that must see past row-level security --
// "is there a row for this alert" -- the way the command's own role cannot.
func AdminConn(t *testing.T, cfg config.Config) *pgx.Conn {
	t.Helper()
	admin, err := url.Parse(os.Getenv("TEST_DATABASE_ADMIN_URL"))
	require.NoError(t, err)
	app, err := url.Parse(cfg.DatabaseURL)
	require.NoError(t, err)
	admin.Path = app.Path
	conn, err := pgx.Connect(context.Background(), admin.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// AdminURL is the connection URL of the superuser for the database cfg points at -- for a
// test that wants a *db.Pool of its own with RLS bypassed (fixtures, assertions).
func AdminURL(t *testing.T, cfg config.Config) string {
	t.Helper()
	admin, err := url.Parse(os.Getenv("TEST_DATABASE_ADMIN_URL"))
	require.NoError(t, err)
	app, err := url.Parse(cfg.DatabaseURL)
	require.NoError(t, err)
	admin.Path = app.Path
	return admin.String()
}

// RunMainUntilHealthy runs a command's runMain in the background with the environment
// CommandConfig already put in place, waits for its /healthz to answer, cancels it, and
// returns the exit status runMain gave. It is how a test exercises the whole startup path
// (load config, validate, run, shut down) in-process.
func RunMainUntilHealthy(t *testing.T, cfg config.Config, runMain func(context.Context, io.Writer) int) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- runMain(ctx, io.Discard) }()

	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case code := <-done:
			t.Fatalf("runMain exited with status %d before becoming healthy", code)
		default:
		}
		resp, err := http.Get("http://" + cfg.HTTPAddr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("runMain did not become healthy within 20s (last error: %v)", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		return code
	case <-time.After(30 * time.Second):
		t.Fatal("runMain did not return within 30s of cancellation")
		return -1
	}
}
