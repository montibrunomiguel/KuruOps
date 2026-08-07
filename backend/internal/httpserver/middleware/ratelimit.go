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
	// Check X-Forwarded-For if behind a proxy
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fallback to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SecurityHeaders applies standard security HTTP headers to responses.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
