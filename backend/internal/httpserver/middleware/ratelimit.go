package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// KeyedLimiter is a generic in-memory sliding-window limiter: N requests per
// window per key. NewRateLimiter (below) wraps one keyed by client IP for
// use as middleware; login also uses one directly, keyed by the submitted
// email, so a distributed brute-force against one account (many source
// IPs) is bounded too, not just a single IP hammering the endpoint -- the
// two limiters are independent and both apply.
type KeyedLimiter struct {
	mu        sync.Mutex
	requests  map[string][]time.Time
	limit     int
	window    time.Duration
	cleanFreq time.Duration
}

// NewKeyedLimiter creates a limiter callers key themselves (e.g. by email,
// user ID, or any other string) via Allow -- for use directly inside a
// handler, not as middleware, since the key often isn't known until the
// request body has been parsed.
func NewKeyedLimiter(limit int, window time.Duration) *KeyedLimiter {
	limiter := &KeyedLimiter{
		requests:  make(map[string][]time.Time),
		limit:     limit,
		window:    window,
		cleanFreq: window * 2,
	}

	// Periodic cleanup of stale entries
	go func() {
		ticker := time.NewTicker(limiter.cleanFreq)
		for range ticker.C {
			limiter.mu.Lock()
			now := time.Now()
			for key, timestamps := range limiter.requests {
				var valid []time.Time
				for _, t := range timestamps {
					if now.Sub(t) <= limiter.window {
						valid = append(valid, t)
					}
				}
				if len(valid) == 0 {
					delete(limiter.requests, key)
				} else {
					limiter.requests[key] = valid
				}
			}
			limiter.mu.Unlock()
		}
	}()

	return limiter
}

// Allow reports whether a request for key is within the limit, recording it
// if so.
func (l *KeyedLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	var valid []time.Time
	for _, t := range l.requests[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= l.limit {
		l.requests[key] = valid
		return false
	}

	valid = append(valid, now)
	l.requests[key] = valid
	return true
}

// NewRateLimiter creates in-memory rate-limiting middleware, keyed by client
// IP. limit: max requests allowed per window duration.
func NewRateLimiter(limit int, window time.Duration) func(http.Handler) http.Handler {
	limiter := NewKeyedLimiter(limit, window)

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
