package llmclient

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kuruops/kuruops/internal/circuitbreaker"
)

// ProviderError is what a provider's non-2xx answer becomes. It used to be an
// anonymous fmt.Errorf, which made every caller that had to decide "is this
// worth trying again?" either guess from the message text or not decide at all
// -- so a single 503 from a briefly overloaded provider ended an analysis for
// good. Carrying the status (and the provider's Retry-After) lets doRequest
// retry in place, and lets the analysis queue decide between "try again in a
// few minutes" and "fix your API key".
//
// Error() is the analyst-facing hint (see providerError); the provider's own
// body is only logged, never part of the error.
type ProviderError struct {
	Status  int
	Message string
	// RetryAfter is the provider's Retry-After header, zero when absent or
	// unparseable.
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string { return e.Message }

// Retryable reports whether sending the same request later can succeed: the
// provider is overloaded or unhealthy (429, 5xx). Every other 4xx says the
// request itself is wrong -- a bad key, URL or model -- and would fail
// identically forever.
func (e *ProviderError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// TransportError is a failure to get any answer at all: refused, reset, DNS,
// timeout. It wraps the cause so errors.Is/As still see it.
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return "request failed: " + e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// Retryable is true for the transient network failures -- a timeout, a refused
// or reset connection, a truncated response, a DNS hiccup -- and false for
// everything else, in particular a caller's own cancellation and httpguard
// refusing a destination (a plain wrapped error, deliberately not a net error:
// retrying a blocked URL can only block it again).
func (e *TransportError) Retryable() bool {
	if errors.Is(e.Err, context.Canceled) {
		return false
	}
	if errors.Is(e.Err, io.EOF) || errors.Is(e.Err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(e.Err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(e.Err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(e.Err, &dnsErr) && (dnsErr.IsTimeout || dnsErr.IsTemporary)
}

// IsRetryable reports whether err describes a failure that may clear up if the
// request is repeated later -- an overloaded or briefly unreachable provider,
// or a circuit breaker that is open and will let a probe through after its
// cooldown. A nil error, a rejected credential, a wrong URL, a malformed
// response and a blocked destination are all not retryable.
func IsRetryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable()
	}
	var te *TransportError
	if errors.As(err, &te) {
		return te.Retryable()
	}
	return errors.Is(err, circuitbreaker.ErrOpen)
}

// ErrorCode is a short, stable identifier for the kind of failure err is, for
// a client that wants to show a message in the user's own language rather than
// the English hint. Empty means "not a provider failure" (a database error, a
// bug), which callers should leave uncoded and show as-is.
func ErrorCode(err error) string {
	var pe *ProviderError
	if errors.As(err, &pe) {
		switch {
		case pe.Status == http.StatusTooManyRequests:
			return "provider_rate_limited"
		case pe.Status == http.StatusUnauthorized || pe.Status == http.StatusForbidden:
			return "provider_auth"
		case pe.Status == http.StatusNotFound:
			return "provider_not_found"
		case pe.Status >= 500:
			return "provider_unavailable"
		default:
			return "provider_error"
		}
	}
	var te *TransportError
	if errors.As(err, &te) {
		return "provider_unreachable"
	}
	if errors.Is(err, circuitbreaker.ErrOpen) {
		return "provider_unavailable"
	}
	return ""
}

// parseRetryAfter reads a Retry-After header, which is either a number of
// seconds or an HTTP date. Zero for anything absent, malformed or in the past.
func parseRetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}
