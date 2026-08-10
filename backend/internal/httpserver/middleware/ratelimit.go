package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// KeyedLimiter is a generic sliding-window limiter backed by a shared
// Postgres table (rate_limit_events): N requests per window per (scope,
// key). Postgres-backed rather than in-memory so the limit is enforced
// across every replica of whichever process constructs one -- an in-memory
// map only ever saw requests handled by its own process, so N replicas
// multiplied the effective limit by N instead of enforcing it coherently.
// scope namespaces keys between the different limiters that share this one
// table (login-by-ip, login-by-email, webhook-by-ip, see NewKeyedLimiter's
// callers) so an IP string and an email string can never collide.
type KeyedLimiter struct {
	pool   *pgxpool.Pool
	scope  string
	limit  int
	window time.Duration
}

// NewKeyedLimiter creates a limiter callers key themselves (e.g. by email,
// user ID, or any other string) via Allow -- for use directly inside a
// handler, not as middleware, since the key often isn't known until the
// request body has been parsed. scope must be unique per call site (see
// KeyedLimiter's doc comment).
func NewKeyedLimiter(pool *pgxpool.Pool, scope string, limit int, window time.Duration) *KeyedLimiter {
	limiter := &KeyedLimiter{pool: pool, scope: scope, limit: limit, window: window}
	go limiter.cleanupLoop()
	return limiter
}

// Allow reports whether a request for key is within the limit, recording it
// if so. Fails open (returns true) on a database error -- a rate limiter
// must never itself become the reason the whole app goes unavailable, and
// in practice every one of these call sites (login, webhook ingestion)
// already depends on the same database for the request to succeed at all,
// so a real outage fails the request downstream regardless.
func (l *KeyedLimiter) Allow(key string) bool {
	ctx := context.Background()
	now := time.Now()
	cutoff := now.Add(-l.window)

	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return true
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if already committed

	// pg_advisory_xact_lock, held for this transaction's lifetime, serializes
	// every Allow() call for this exact (scope, key) across concurrent
	// requests and replicas -- without it, the count-then-insert below is a
	// classic TOCTOU race under Postgres's default READ COMMITTED isolation:
	// N concurrent transactions can all read "count < limit" before any of
	// them commits its insert, letting the limit be exceeded by up to N.
	// Same advisory-lock idiom already used for worker sweep coordination
	// (see db.Pool.WithAdvisoryLock), just inline here since KeyedLimiter
	// only holds a raw *pgxpool.Pool, not the wrapped db.Pool. hashtext
	// collapses (scope, key) into a single lock key -- an occasional hash
	// collision between two different (scope, key) pairs only costs a
	// harmless bit of serialization between otherwise-unrelated keys, never
	// a correctness problem.
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtext($1 || ':' || $2))", l.scope, key); err != nil {
		return true
	}

	// Deletes this key's own stale rows first -- keeps the common case
	// self-cleaning without waiting on cleanupLoop's periodic sweep, which
	// only needs to catch keys that stop being checked entirely (e.g. an IP
	// that's rate-limited once and never returns).
	if _, err := tx.Exec(ctx, "delete from rate_limit_events where scope = $1 and key = $2 and occurred_at <= $3", l.scope, key, cutoff); err != nil {
		return true
	}

	var count int
	if err := tx.QueryRow(ctx, "select count(*) from rate_limit_events where scope = $1 and key = $2", l.scope, key).Scan(&count); err != nil {
		return true
	}

	if count >= l.limit {
		_ = tx.Commit(ctx) //nolint:errcheck // still commit the cleanup delete above; a failed commit here just leaves stale rows for next time
		return false
	}

	if _, err := tx.Exec(ctx, "insert into rate_limit_events (scope, key, occurred_at) values ($1, $2, $3)", l.scope, key, now); err != nil {
		return true
	}
	if err := tx.Commit(ctx); err != nil {
		return true
	}
	return true
}

// cleanupLoop periodically deletes this limiter's stale rows across ALL
// keys -- Allow only ever cleans the one key it was just called with, so
// this is what keeps the table from growing unboundedly for keys that stop
// being checked.
func (l *KeyedLimiter) cleanupLoop() {
	ticker := time.NewTicker(l.window * 2)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-l.window)
		_, _ = l.pool.Exec(context.Background(), "delete from rate_limit_events where scope = $1 and occurred_at <= $2", l.scope, cutoff)
	}
}

// NewRateLimiter creates Postgres-backed rate-limiting middleware, keyed by
// client IP. limit: max requests allowed per window duration. scope must be
// unique per call site (see KeyedLimiter's doc comment).
func NewRateLimiter(pool *pgxpool.Pool, scope string, limit int, window time.Duration) func(http.Handler) http.Handler {
	limiter := NewKeyedLimiter(pool, scope, limit, window)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getClientIP(r)
			if !limiter.Allow(ip) {
				http.Error(w, "too many requests - rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func getClientIP(r *http.Request) string {
	// Only X-Real-IP is trusted, and only because nginx.conf sets it
	// unconditionally on every proxied request (proxy_set_header X-Real-IP
	// $remote_addr) -- it always overwrites whatever the client sent, so a
	// forged inbound X-Real-IP never survives the hop through nginx.
	// X-Forwarded-For is deliberately NOT consulted: it's client-suppliable,
	// and trusting it (as this function used to, and as chi's now-deprecated
	// RealIP middleware still does) let an attacker bypass this exact rate
	// limiter by sending a fresh X-Forwarded-For on every request. See
	// GHSA-3fxj-6jh8-hvhx / GO-2026-5777 / GO-2026-5775.
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}

	// No trusted proxy header present -- direct connection (dev, or nginx
	// not in front) or a test harness. Fall back to the TCP-level RemoteAddr.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
