package db_test

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
)

func requireTestDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set -- run via `task backend:test:integration`")
	}
	return url
}

func TestNewPool_InvalidURL(t *testing.T) {
	_, err := db.NewPool(t.Context(), "not a valid connection string")
	assert.Error(t, err)
}

func TestNewPool_UnreachableHost(t *testing.T) {
	_, err := db.NewPool(t.Context(), "postgres://user:pass@127.0.0.1:1/nonexistent?connect_timeout=1")
	assert.Error(t, err, "Ping during NewPool must surface an unreachable database immediately")
}

func TestPool_WithTenant(t *testing.T) {
	pool, err := db.NewPool(t.Context(), requireTestDatabaseURL(t))
	require.NoError(t, err)
	defer pool.Close()

	tenantID := uuid.New()

	t.Run("fn's queries run with app.tenant_id set", func(t *testing.T) {
		var got string
		err := pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), "select current_setting('app.tenant_id', true)").Scan(&got)
		})
		require.NoError(t, err)
		assert.Equal(t, tenantID.String(), got)
	})

	t.Run("an error from fn rolls back and propagates", func(t *testing.T) {
		sentinel := assert.AnError
		err := pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			return sentinel
		})
		assert.ErrorIs(t, err, sentinel)
	})

	t.Run("tenant context does not leak to a later call on a reused connection", func(t *testing.T) {
		otherTenant := uuid.New()
		var got string
		err := pool.WithTenant(t.Context(), otherTenant, func(tx pgx.Tx) error {
			return tx.QueryRow(t.Context(), "select current_setting('app.tenant_id', true)").Scan(&got)
		})
		require.NoError(t, err)
		assert.Equal(t, otherTenant.String(), got)
		assert.NotEqual(t, tenantID.String(), got)
	})
}
