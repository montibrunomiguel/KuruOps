package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/db"
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
	_, err := db.NewPool(t.Context(), "not a valid connection string", db.PoolConfig{})
	assert.Error(t, err)
}

func TestNewPool_UnreachableHost(t *testing.T) {
	_, err := db.NewPool(t.Context(), "postgres://user:pass@127.0.0.1:1/nonexistent?connect_timeout=1", db.PoolConfig{})
	assert.Error(t, err, "Ping during NewPool must surface an unreachable database immediately")
}

func TestNewPool_PoolConfigOverridesSizing(t *testing.T) {
	pool, err := db.NewPool(t.Context(), requireTestDatabaseURL(t), db.PoolConfig{MaxConns: 7, MinConns: 2})
	require.NoError(t, err)
	defer pool.Close()

	assert.EqualValues(t, 7, pool.Config().MaxConns)
	assert.EqualValues(t, 2, pool.Config().MinConns)
}

func TestNewPool_ZeroPoolConfigLeavesDefaults(t *testing.T) {
	pool, err := db.NewPool(t.Context(), requireTestDatabaseURL(t), db.PoolConfig{})
	require.NoError(t, err)
	defer pool.Close()

	// pgxpool's own default (greater of 4 or runtime.NumCPU()) -- just
	// confirm it's non-zero and wasn't forced down to 0 by an unconditional
	// assignment.
	assert.Positive(t, pool.Config().MaxConns)
}

func TestPool_WithTenant(t *testing.T) {
	pool, err := db.NewPool(t.Context(), requireTestDatabaseURL(t), db.PoolConfig{})
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

func TestPool_WithAdvisoryLock(t *testing.T) {
	// Two independent pools against the same database, standing in for two
	// separate process replicas each holding their own connections -- a
	// single shared pool would still prove the locking works, but this is
	// closer to what actually happens in production (cmd/worker replica A
	// vs replica B, never the same *pgxpool.Pool).
	poolA, err := db.NewPool(t.Context(), requireTestDatabaseURL(t), db.PoolConfig{})
	require.NoError(t, err)
	defer poolA.Close()
	poolB, err := db.NewPool(t.Context(), requireTestDatabaseURL(t), db.PoolConfig{})
	require.NoError(t, err)
	defer poolB.Close()

	key := int64(-990001) // test-only key, distinct from cmd/worker's real ones

	t.Run("acquires when free and runs fn", func(t *testing.T) {
		var ran bool
		acquired, err := poolA.WithAdvisoryLock(t.Context(), key, func(context.Context) error {
			ran = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, acquired)
		assert.True(t, ran)
	})

	t.Run("a second acquirer skips the same key while the first still holds it, then can acquire once released", func(t *testing.T) {
		holding := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})

		var firstRan bool
		go func() {
			defer close(done)
			acquired, err := poolA.WithAdvisoryLock(context.Background(), key, func(context.Context) error {
				firstRan = true
				close(holding)
				<-release
				return nil
			})
			assert.NoError(t, err)
			assert.True(t, acquired)
		}()

		<-holding
		var secondRan bool
		acquired, err := poolB.WithAdvisoryLock(t.Context(), key, func(context.Context) error {
			secondRan = true
			return nil
		})
		require.NoError(t, err)
		assert.False(t, acquired, "a concurrent holder of the same key must block a second acquirer")
		assert.False(t, secondRan)

		close(release)
		<-done
		assert.True(t, firstRan)

		var thirdRan bool
		acquired, err = poolB.WithAdvisoryLock(t.Context(), key, func(context.Context) error {
			thirdRan = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, acquired, "once the holder's transaction ends the lock must be free again")
		assert.True(t, thirdRan)
	})

	t.Run("an error from fn still releases the lock (rollback ends the transaction)", func(t *testing.T) {
		sentinel := assert.AnError
		acquired, err := poolA.WithAdvisoryLock(t.Context(), key, func(context.Context) error {
			return sentinel
		})
		assert.True(t, acquired)
		assert.ErrorIs(t, err, sentinel)

		var ran bool
		acquired, err = poolB.WithAdvisoryLock(t.Context(), key, func(context.Context) error {
			ran = true
			return nil
		})
		require.NoError(t, err)
		assert.True(t, acquired, "a failed fn must not leave the lock stuck held")
		assert.True(t, ran)
	})
}
