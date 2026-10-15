package llmclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/circuitbreaker"
)

// Every test in this binary (internal and external package alike) shares these
// package variables, so shrink the waits once for all of them: tests that hit a
// failing server would otherwise sleep through real one- and two-second
// backoffs. The tests below that care about the actual delay calculation call
// retryDelay directly and set what they need.
func init() {
	retryBaseDelay = time.Millisecond
	retryMaxDelay = 4 * time.Millisecond
}

// newTestClient points an OpenAI-compatible client at srv.
func newTestClient(t *testing.T, srv *httptest.Server) Client {
	t.Helper()
	base := srv.URL
	c, err := New("openai_compatible", &base, "sk-test", "m")
	require.NoError(t, err)
	return c
}

const okBody = `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`

func TestDoRequest_RetriesATransientProviderFailure(t *testing.T) {
	var hits atomic.Int32
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":503,"message":"high demand"}}`)
			return
		}
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Complete(t.Context(), "sys", "analyse this")
	require.NoError(t, err, "two 503s followed by a 200 is an analysis that should have succeeded")
	assert.Equal(t, "ok", got)
	assert.EqualValues(t, 3, hits.Load())

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, bodies, 3)
	assert.NotEmpty(t, bodies[0])
	assert.Equal(t, bodies[0], bodies[1], "the request body must be sent again intact, not drained by the first attempt")
	assert.Equal(t, bodies[0], bodies[2])
}

func TestDoRequest_GivesUpAfterTheMaximumAttempts(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Complete(t.Context(), "sys", "x")
	require.Error(t, err)
	assert.EqualValues(t, retryMaxAttempts, hits.Load(), "exactly the configured number of attempts, no more")

	var pe *ProviderError
	require.ErrorAs(t, err, &pe, "the failure that survives retrying must still be the typed provider error")
	assert.Equal(t, http.StatusBadGateway, pe.Status)
	assert.True(t, IsRetryable(err), "still worth retrying later -- by the analysis queue, on a longer schedule")
}

func TestDoRequest_DoesNotRetryAFailureThatWillRepeat(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(status)
			}))
			defer srv.Close()

			_, err := newTestClient(t, srv).Complete(t.Context(), "sys", "x")
			require.Error(t, err)
			assert.EqualValues(t, 1, hits.Load(), "a bad key/URL/model fails identically every time; retrying only delays the message")
			assert.False(t, IsRetryable(err))
		})
	}
}

func TestDoRequest_RetriesADroppedConnection(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			// Hang up without answering: what a provider restarting mid-request
			// looks like from the client.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test server cannot hijack")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Complete(t.Context(), "sys", "x")
	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	assert.EqualValues(t, 2, hits.Load())
}

func TestDoRequest_StopsWaitingWhenTheCallerGoesAway(t *testing.T) {
	prevBase, prevMax := retryBaseDelay, retryMaxDelay
	retryBaseDelay, retryMaxDelay = 5*time.Second, 5*time.Second
	defer func() { retryBaseDelay, retryMaxDelay = prevBase, prevMax }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := newTestClient(t, srv).Complete(ctx, "sys", "x")

	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "must not sit out a multi-second backoff for a caller that has already left")
	var pe *ProviderError
	assert.ErrorAs(t, err, &pe, "the useful error is the provider's, not a deadline-exceeded")
}

func TestRetryDelay(t *testing.T) {
	prevBase, prevMax, prevCeil := retryBaseDelay, retryMaxDelay, retryAfterCeiling
	retryBaseDelay, retryMaxDelay, retryAfterCeiling = 100*time.Millisecond, 800*time.Millisecond, 15*time.Second
	defer func() { retryBaseDelay, retryMaxDelay, retryAfterCeiling = prevBase, prevMax, prevCeil }()

	t.Run("backs off exponentially with jitter, capped", func(t *testing.T) {
		for attempt, hi := range map[int]time.Duration{1: 100 * time.Millisecond, 2: 200 * time.Millisecond} {
			for i := 0; i < 50; i++ {
				d, ok := retryDelay(&ProviderError{Status: 503}, attempt)
				require.True(t, ok)
				assert.GreaterOrEqual(t, d, hi/2, "jitter keeps at least half the nominal delay")
				assert.LessOrEqual(t, d, hi)
			}
		}
		assert.LessOrEqual(t, backoff(30), retryMaxDelay, "an enormous attempt number must not overflow or exceed the cap")
		assert.Greater(t, backoff(30), time.Duration(0))
	})

	t.Run("honours Retry-After when it is longer than the backoff", func(t *testing.T) {
		d, ok := retryDelay(&ProviderError{Status: 429, RetryAfter: 3 * time.Second}, 1)
		require.True(t, ok)
		assert.Equal(t, 3*time.Second, d)
	})

	t.Run("does not wait out a Retry-After longer than the ceiling", func(t *testing.T) {
		_, ok := retryDelay(&ProviderError{Status: 429, RetryAfter: time.Minute}, 1)
		assert.False(t, ok, "a minute is for the analysis queue to wait, not one analysis turn")
	})

	t.Run("stops at the maximum attempts", func(t *testing.T) {
		_, ok := retryDelay(&ProviderError{Status: 503}, retryMaxAttempts)
		assert.False(t, ok)
	})

	t.Run("an open circuit breaker is not retried in place", func(t *testing.T) {
		_, ok := retryDelay(fmt.Errorf("%w for host: next attempt in 30s", circuitbreaker.ErrOpen), 1)
		assert.False(t, ok, "hammering an open breaker from a retry loop defeats the breaker")
	})

	t.Run("a non-provider error is not retried", func(t *testing.T) {
		_, ok := retryDelay(errors.New("encode request"), 1)
		assert.False(t, ok)
	})
}

func TestIsRetryableAndErrorCode(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
		code      string
	}{
		{"nil", nil, false, ""},
		{"503", &ProviderError{Status: 503}, true, "provider_unavailable"},
		{"500", &ProviderError{Status: 500}, true, "provider_unavailable"},
		{"429", &ProviderError{Status: 429}, true, "provider_rate_limited"},
		{"401", &ProviderError{Status: 401}, false, "provider_auth"},
		{"403", &ProviderError{Status: 403}, false, "provider_auth"},
		{"404", &ProviderError{Status: 404}, false, "provider_not_found"},
		{"400", &ProviderError{Status: 400}, false, "provider_error"},
		{"wrapped, as the agent loop wraps it", fmt.Errorf("llm analysis: %w", &ProviderError{Status: 503}), true, "provider_unavailable"},
		{"breaker open", fmt.Errorf("%w for h", circuitbreaker.ErrOpen), true, "provider_unavailable"},
		{"a client timeout is transient", &TransportError{Err: context.DeadlineExceeded}, true, "provider_unreachable"},
		{"the caller cancelling is not", &TransportError{Err: context.Canceled}, false, "provider_unreachable"},
		{"a database error", errors.New("insert: connection refused"), false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.retryable, IsRetryable(tc.err))
			assert.Equal(t, tc.code, ErrorCode(tc.err))
		})
	}

	t.Run("a blocked destination is never retried", func(t *testing.T) {
		// httpguard refuses with a plain wrapped error, not a net error;
		// retrying it can only be refused again.
		blocked := &TransportError{Err: errors.New("httpguard: 10.0.0.5 is a private address")}
		assert.False(t, IsRetryable(blocked))
	})
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, 7*time.Second, parseRetryAfter("7", now))
	assert.Equal(t, 90*time.Second, parseRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now))
	assert.Zero(t, parseRetryAfter("", now))
	assert.Zero(t, parseRetryAfter("soon", now))
	assert.Zero(t, parseRetryAfter("-5", now))
	assert.Zero(t, parseRetryAfter(now.Add(-time.Hour).Format(http.TimeFormat), now), "a date in the past means retry now, not a negative wait")
}
