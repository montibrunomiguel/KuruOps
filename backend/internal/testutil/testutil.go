// Package testutil provides shared fixtures for integration tests that need
// a real Postgres connection. Repository methods take a pgx.Tx directly
// (never a bare pool) and RLS policies only mean anything against a real
// database, so unlike the rest of this codebase's tests, these can't be
// mocked away -- see db/migrations/0008_row_level_security.up.sql.
//
// Two pools are used deliberately:
//   - the "app" pool authenticates as argusops_app, the same least-privilege,
//     RLS-subject role the running services connect as (see
//     db/init/argusops_app_role.sql) -- this is what code under test uses.
//   - the "admin" pool authenticates as the postgres superuser, which owns
//     every table and therefore bypasses RLS unconditionally -- this is
//     used ONLY to set up fixtures (a tenant, a user) before a test starts,
//     never by the code under test itself.
package testutil

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/db"
)

// testPassword is the fixed plaintext behind every fixture user's stored
// hash -- tests that exercise login use this directly rather than hashing
// per-call, since HashPassword is intentionally slow (argon2id).
const testPassword = "Testpass123!"

var testPasswordHash = mustHashTestPassword()

func mustHashTestPassword() string {
	hash, err := authn.HashPassword(testPassword)
	if err != nil {
		panic("testutil: hash fixture password: " + err.Error())
	}
	return hash
}

const (
	envAppURL   = "TEST_DATABASE_URL"
	envAdminURL = "TEST_DATABASE_ADMIN_URL"
)

// RequireTestDB connects to TEST_DATABASE_URL (see Taskfile.yml's
// db:test:up / backend:test:integration tasks) and skips the calling test
// if it isn't set, so `go test ./...` still runs the pure unit tests
// without a database and only the integration suite needs
// `task backend:test:integration`.
func RequireTestDB(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv(envAppURL)
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set -- run via `task backend:test:integration`")
	}
	pool, err := db.NewPool(context.Background(), url, db.PoolConfig{})
	if err != nil {
		t.Fatalf("connect to test database (app role): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// adminPool connects with superuser privileges, for fixture setup that must
// bypass RLS (creating the tenant/user rows a test's real RLS-scoped calls
// will then operate on).
func adminPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv(envAdminURL)
	if url == "" {
		t.Skip("TEST_DATABASE_ADMIN_URL not set -- run via `task backend:test:integration`")
	}
	pool, err := db.NewPool(context.Background(), url, db.PoolConfig{})
	if err != nil {
		t.Fatalf("connect to test database (admin role): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewTenant inserts a throwaway tenant with a random slug, giving each test
// natural isolation under RLS without needing explicit row cleanup between
// runs -- a query scoped to this tenant can never see another test's rows.
func NewTenant(t *testing.T) uuid.UUID {
	t.Helper()
	pool := adminPool(t)
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`insert into tenants (id, name, slug) values ($1, $2, $3)`,
		id, "test-"+id.String(), id.String(),
	)
	if err != nil {
		t.Fatalf("create test tenant: %v", err)
	}
	return id
}

// TestPassword is the plaintext behind every fixture user created by
// NewUser -- use it directly in login-flow tests instead of hashing a new
// one (HashPassword is intentionally slow).
const TestPassword = testPassword

// NewRole inserts a throwaway Role and returns its id -- every NewUser call
// gets its own fresh Role row (see NewUser) rather than one shared per
// tenant, since tests only ever assert on a role's capability content
// (IsAdmin/ResourceAccess/AllowedTags), never on how many role rows a
// tenant has or whether two users share one.
func NewRole(t *testing.T, tenantID uuid.UUID, isAdmin bool, resourceAccess []string) uuid.UUID {
	t.Helper()
	pool := adminPool(t)
	if resourceAccess == nil {
		resourceAccess = []string{}
	}
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		insert into roles (id, tenant_id, name, is_admin, resource_access, allowed_tags)
		values ($1, $2, $3, $4, $5, '{}')`,
		id, tenantID, "test-role-"+id.String(), isAdmin, resourceAccess,
	)
	if err != nil {
		t.Fatalf("create test role: %v", err)
	}
	return id
}

// NewUser inserts a local, active, password-set user (auth_provider='local'
// requires a password hash -- see the users_local_requires_password check
// constraint in db/migrations/0003_tenants_users.up.sql), backed by a fresh
// Role built from role/resourceAccess -- role is "admin" (grants
// Role.IsAdmin) or anything else (not admin, purely a label on the
// generated Role's name, since IsAdmin is the only tier distinction that
// still means anything -- see domain.Role's doc comment). resourceAccess
// follows domain.ResourceAccess (e.g. []string{"alerts","incidents"}); pass
// nil for none.
func NewUser(t *testing.T, tenantID uuid.UUID, role string, resourceAccess []string) uuid.UUID {
	t.Helper()
	roleID := NewRole(t, tenantID, role == "admin", resourceAccess)
	return NewUserWithRole(t, tenantID, roleID)
}

// NewUserWithRole is NewUser for callers that already have a specific Role
// id to assign (e.g. testing that a role can't be deleted while a user
// still references it) instead of wanting one generated on the fly.
func NewUserWithRole(t *testing.T, tenantID, roleID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := adminPool(t)
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		insert into users (id, tenant_id, email, name, auth_provider, password_hash, role_id, is_active)
		values ($1, $2, $3, 'Test User', 'local', $4, $5, true)`,
		id, tenantID, fmt.Sprintf("%s@test.local", id), testPasswordHash, roleID,
	)
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return id
}

// NewWebhookEndpoint inserts a throwaway webhook endpoint -- alerts.webhook_endpoint_id
// is a real foreign key, so any test that ingests an alert needs one of
// these to already exist and be committed (AlertService.Ingest opens its
// own transaction via db.Pool.WithTenant, separate from whatever fixture
// setup created the tenant/user, so the row must be visible to a fresh
// connection, not just held in an uncommitted tx).
func NewWebhookEndpoint(t *testing.T, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	pool := adminPool(t)
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		insert into webhook_endpoints (id, tenant_id, name, source, token_hash, token_last4)
		values ($1, $2, $3, 'test', $3, 'test')`,
		id, tenantID, id.String(),
	)
	if err != nil {
		t.Fatalf("create test webhook endpoint: %v", err)
	}
	return id
}

// BeginTx opens a transaction against the app (RLS-subject) pool with
// app.tenant_id set for tenantID, matching what db.Pool.WithTenant does
// per-request -- for repository-level tests that call a repo method
// directly with a pgx.Tx. Rolled back automatically at test cleanup, so
// nothing written through it needs manual deletion.
func BeginTx(t *testing.T, pool *db.Pool, tenantID uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, "select set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("set tenant context: %v", err)
	}
	return tx
}
