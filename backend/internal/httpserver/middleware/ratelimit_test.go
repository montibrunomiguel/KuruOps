package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/testutil"
)

// uniqueScope returns a scope string that's unique to this call -- Allow is
// now backed by a shared Postgres table (see KeyedLimiter), not a fresh
// in-memory map per test process, so a fixed literal scope would collide
// with this same test's own rows from a previous run within the window
// (the table isn't reset between individual `go test` invocations, only by
// `task db:test:reset`).
func uniqueScope(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func TestKeyedLimiter_Allow(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	limiter := middleware.NewKeyedLimiter(t.Context(), pool.Pool, uniqueScope("test_keyed_limiter_allow"), 2, time.Minute)

	assert.True(t, limiter.Allow("a@test.local"), "1st request for this key is allowed")
	assert.True(t, limiter.Allow("a@test.local"), "2nd request for this key is allowed")
	assert.False(t, limiter.Allow("a@test.local"), "3rd request exceeds the limit")

	t.Run("a different key has its own independent budget", func(t *testing.T) {
		assert.True(t, limiter.Allow("b@test.local"))
		assert.True(t, limiter.Allow("b@test.local"))
		assert.False(t, limiter.Allow("b@test.local"))
	})
}

func TestKeyedLimiter_ScopesAreIndependent(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	// Same key, two different limiter scopes -- proves an IP string used by
	// one limiter (e.g. login-by-ip) can never collide with the same string
	// used as a key by a different limiter (e.g. webhook-by-ip).
	limiterA := middleware.NewKeyedLimiter(t.Context(), pool.Pool, uniqueScope("test_scope_a"), 1, time.Minute)
	limiterB := middleware.NewKeyedLimiter(t.Context(), pool.Pool, uniqueScope("test_scope_b"), 1, time.Minute)

	assert.True(t, limiterA.Allow("shared-key"))
	assert.False(t, limiterA.Allow("shared-key"), "scope A's own budget is now exhausted")
	assert.True(t, limiterB.Allow("shared-key"), "scope B has its own independent budget for the same key string")
}

// TestKeyedLimiter_SharedAcrossReplicas is the regression test for the
// whole point of this rewrite: two independent *KeyedLimiter instances
// against the same database (standing in for two process replicas, each
// with their own in-memory state under the old design) must enforce ONE
// combined budget for the same key, not double it.
func TestKeyedLimiter_SharedAcrossReplicas(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	scope := uniqueScope("test_shared_across_replicas")
	replicaA := middleware.NewKeyedLimiter(t.Context(), pool.Pool, scope, 2, time.Minute)
	replicaB := middleware.NewKeyedLimiter(t.Context(), pool.Pool, scope, 2, time.Minute)

	assert.True(t, replicaA.Allow("10.0.0.9"), "1st request, seen by replica A")
	assert.True(t, replicaB.Allow("10.0.0.9"), "2nd request, seen by replica B -- still within the combined limit of 2")
	assert.False(t, replicaA.Allow("10.0.0.9"), "3rd request, back on replica A -- the limit is shared, not per-replica")
	assert.False(t, replicaB.Allow("10.0.0.9"), "4th request, on replica B -- still rejected")
}

// TestKeyedLimiter_Allow_ConcurrentRequestsNeverExceedLimit is the
// regression test for the TOCTOU race the pg_advisory_xact_lock in Allow
// closes: without it, count-then-insert isn't atomic under Postgres's
// default READ COMMITTED isolation -- N concurrent transactions for the
// same key can all read "count < limit" before any of them commits its
// insert, letting more than `limit` requests through in the same window.
// Fires many goroutines at once (via a start-gate channel to maximize the
// race window, same technique as
// cmd/worker.TestRunLocked_OnlyOneReplicaExecutesConcurrently) against a
// small limit and asserts the total number of allowed requests never
// exceeds it.
func TestKeyedLimiter_Allow_ConcurrentRequestsNeverExceedLimit(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	limiter := middleware.NewKeyedLimiter(t.Context(), pool.Pool, uniqueScope("test_concurrent_allow"), 3, time.Minute)

	const concurrency = 20
	start := make(chan struct{})
	var wg sync.WaitGroup
	var allowed atomic.Int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if limiter.Allow("10.0.0.99") {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.LessOrEqual(t, allowed.Load(), int64(3), "no more than the configured limit may be allowed even under concurrent requests for the same key")
}

func TestNewRateLimiter_PerIP(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	limiter := middleware.NewRateLimiter(t.Context(), pool.Pool, uniqueScope("test_per_ip"), 2, time.Minute)
	handler := limiter(okHandler())

	newReq := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	assert.Equal(t, http.StatusTooManyRequests, newReq("10.0.0.1").Code, "3rd request from the same IP is rejected")

	assert.Equal(t, http.StatusOK, newReq("10.0.0.2").Code, "a different IP has its own independent budget")
}

func TestNewRateLimiter_IgnoresSpoofedXForwardedFor(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	limiter := middleware.NewRateLimiter(t.Context(), pool.Pool, uniqueScope("test_ignores_xff"), 2, time.Minute)
	handler := limiter(okHandler())

	// Same RemoteAddr (as if a single client connected to nginx once), but a
	// different X-Forwarded-For on every request -- if XFF were trusted (the
	// bug this test guards against), each request would get its own budget
	// and the limiter would never trigger.
	newReq := func(spoofedXFF string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", spoofedXFF)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, newReq("1.1.1.1").Code)
	require.Equal(t, http.StatusOK, newReq("2.2.2.2").Code)
	assert.Equal(t, http.StatusTooManyRequests, newReq("3.3.3.3").Code, "still rate limited despite a different spoofed X-Forwarded-For on every request")
}

func TestNewRateLimiter_TrustsXRealIPFromProxy(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	limiter := middleware.NewRateLimiter(t.Context(), pool.Pool, uniqueScope("test_trusts_x_real_ip"), 2, time.Minute)
	handler := limiter(okHandler())

	// nginx.conf sets X-Real-IP unconditionally, so it's the trusted signal
	// -- two different X-Real-IP values (as nginx would set for two distinct
	// real clients) get independent budgets even from the same RemoteAddr
	// (nginx's own connection to the backend).
	newReq := func(realIP string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		req.Header.Set("X-Real-IP", realIP)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	require.Equal(t, http.StatusOK, newReq("10.0.0.1").Code)
	assert.Equal(t, http.StatusTooManyRequests, newReq("10.0.0.1").Code, "3rd request from the same X-Real-IP is rejected")

	assert.Equal(t, http.StatusOK, newReq("10.0.0.2").Code, "a different X-Real-IP has its own independent budget")
}
