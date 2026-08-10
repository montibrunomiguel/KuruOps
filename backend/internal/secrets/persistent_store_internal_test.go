package secrets

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/testutil"
)

// testEncryptionKey mirrors persistent_store_test.go's const of the same
// name -- duplicated rather than shared because that one lives in
// secrets_test (external test package), unreachable from here.
const testEncryptionKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="

// TestPersistentEnvStore_RefreshLoopPropagatesAcrossReplicas is the
// regression test for Frente 4: two PersistentEnvStore instances against
// the same pool (standing in for two api/ingest replicas) must converge on
// a value Put on only one of them, within one refresh tick -- before the
// periodic refreshLoop existed, Resolve on the second instance would have
// returned "" forever, since it only ever read the map built once at
// construction. Lives in-package (not secrets_test) so it can shrink the
// package-level refreshInterval var to keep this fast instead of waiting on
// the real 60s production interval.
func TestPersistentEnvStore_RefreshLoopPropagatesAcrossReplicas(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	ctx := t.Context()

	original := refreshInterval
	refreshInterval = 100 * time.Millisecond
	t.Cleanup(func() { refreshInterval = original })

	replicaA, err := NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)
	replicaB, err := NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)

	ref, err := replicaA.Put(ctx, "tenant-refresh", "webhook-secret", "whsec_v1")
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		value, err := replicaB.Resolve(ctx, ref)
		return err == nil && value == "whsec_v1"
	}, 2*time.Second, 20*time.Millisecond, "replica B should see the value Put on replica A within a few refresh ticks")
}

// TestPersistentEnvStore_RefreshLoop_ExitsOnContextCancelAndLogsFetchErrors
// covers refreshLoop's other two branches, neither exercised by the happy
// path above: returning once its context is cancelled (so it doesn't leak a
// goroutine for the process's lifetime), and surviving a fetchAll error by
// logging and keeping whatever it last successfully loaded instead of
// panicking or propagating the error anywhere Resolve would see it.
func TestPersistentEnvStore_RefreshLoop_ExitsOnContextCancelAndLogsFetchErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)

	original := refreshInterval
	refreshInterval = 10 * time.Millisecond
	t.Cleanup(func() { refreshInterval = original })

	// Build the store with an already-cancelled context so the
	// goroutine NewPersistentEnvStore starts internally exits
	// immediately -- otherwise it would keep running against the same
	// *store.pool* field this test mutates below, racing with it.
	ctx, cancel := context.WithCancel(t.Context())
	store, err := NewPersistentEnvStore(ctx, pool, testEncryptionKey)
	require.NoError(t, err)
	cancel()
	time.Sleep(20 * time.Millisecond)

	workerURL := os.Getenv("TEST_DATABASE_WORKER_URL")
	if workerURL == "" {
		t.Skip("TEST_DATABASE_WORKER_URL not set -- run via `task backend:test:integration`")
	}
	brokenPool, err := db.NewPool(context.Background(), workerURL, db.PoolConfig{})
	require.NoError(t, err)
	brokenPool.Close()
	store.pool = brokenPool

	loopCtx, loopCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		store.refreshLoop(loopCtx)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond) // let at least one tick hit the closed pool without panicking
	loopCancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("refreshLoop did not return after its context was cancelled")
	}
}
