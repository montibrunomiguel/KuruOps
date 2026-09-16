// Package circuitbreaker provides a small, dependency-free circuit breaker
// for calls out to external providers (LLM APIs, MCP servers).
//
// The problem it solves is not a single failed call -- that already returns
// an error -- but a provider that has gone bad and stays bad. Without a
// breaker, every AI analysis run keeps dialing a dead endpoint and waits
// out the full HTTP timeout before failing, so one unhealthy provider
// occupies worker goroutines and DB connections for as long as it stays
// down. Failing fast turns a 30-second hang into an immediate, clearly
// labelled error.
//
// Breakers are keyed (see Registry) rather than global: one provider being
// down must not fail calls to a different one.
package circuitbreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrOpen is returned instead of calling the wrapped function while the
// breaker for that key is open. Callers can test for it with errors.Is to
// distinguish "we didn't even try" from a real provider error -- which
// matters for the message an analyst sees.
var ErrOpen = errors.New("circuit breaker is open")

// faultError marks an error as a provider fault -- something that says the
// provider itself is unhealthy, and should therefore count towards tripping
// the breaker.
//
// The distinction matters more than it might look. A 401 from a wrong API
// key and a 400 from a malformed request fail every single time, so
// counting them would trip the breaker permanently and replace a precise,
// actionable error ("invalid x-api-key") with a generic "circuit breaker is
// open" -- turning a five-second fix into a debugging session. Only
// transport failures, 5xx and 429 are faults; see the call sites in
// llmclient and mcpclient.
type faultError struct{ err error }

func (f faultError) Error() string { return f.err.Error() }
func (f faultError) Unwrap() error { return f.err }

// Fault marks err as a provider fault, so a Registry counts it against the
// breaker. A nil err stays nil.
func Fault(err error) error {
	if err == nil {
		return nil
	}
	return faultError{err: err}
}

// IsFault reports whether err was marked by Fault anywhere in its chain.
func IsFault(err error) bool {
	var f faultError
	return errors.As(err, &f)
}

// Config tunes the breaker. The zero value disables breaking entirely
// (Threshold 0), which is what every test and any operator who wants the
// old behaviour back gets by leaving the env vars unset -- see
// config.Config.
type Config struct {
	// Threshold is how many consecutive faults open the breaker. 0 disables
	// the breaker: every call goes through, exactly as before.
	Threshold int
	// Cooldown is how long the breaker stays open before letting a single
	// probe through.
	Cooldown time.Duration
}

// Enabled reports whether this config actually breaks anything.
func (c Config) Enabled() bool { return c.Threshold > 0 && c.Cooldown > 0 }

type state int

const (
	closed state = iota
	open
	halfOpen
)

type breaker struct {
	mu       sync.Mutex
	state    state
	failures int
	openedAt time.Time
	// probing is set while a half-open probe is in flight, so a burst of
	// concurrent calls sends exactly one request at the recovering provider
	// instead of all of them at once.
	probing bool
}

// Registry holds one breaker per key and is safe for concurrent use.
type Registry struct {
	cfg Config
	mu  sync.Mutex
	m   map[string]*breaker
}

func NewRegistry(cfg Config) *Registry {
	return &Registry{cfg: cfg, m: make(map[string]*breaker)}
}

func (r *Registry) get(key string) *breaker {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.m[key]
	if !ok {
		b = &breaker{}
		r.m[key] = b
	}
	return b
}

// Do runs fn unless the breaker for key is open, in which case it returns
// ErrOpen without calling fn at all.
//
// Only errors marked with Fault count against the breaker; any other error
// is passed straight back and leaves the breaker's state untouched, since
// it says nothing about the provider's health.
func (r *Registry) Do(key string, fn func() error) error {
	if !r.cfg.Enabled() {
		return fn()
	}
	b := r.get(key)

	allowed, probe, retryIn := b.allow(r.cfg.Cooldown)
	if !allowed {
		if retryIn == 0 {
			// Refused because a probe is already in flight, not because a
			// timer is still running -- saying "in 0s" would read as a bug.
			return fmt.Errorf("%w for %s: provider looks unhealthy, a recovery probe is in flight", ErrOpen, key)
		}
		return fmt.Errorf("%w for %s: provider looks unhealthy, next attempt in %s",
			ErrOpen, key, retryIn.Round(time.Second))
	}

	err := fn()
	if IsFault(err) {
		b.onFault(r.cfg.Threshold, probe)
	} else {
		b.onSuccess(probe)
	}
	return err
}

// allow decides whether a call may proceed, whether it is the single
// half-open probe (the only call whose result may close the breaker again),
// and -- when it refuses -- how long the caller should expect to wait.
//
// All three come out of one critical section on purpose: computing the
// retry hint under a second Lock would let the state change in between and
// report a delay that never matched the decision it explains.
func (b *breaker) allow(cooldown time.Duration) (allowed, probe bool, retryIn time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	remaining := cooldown - time.Since(b.openedAt)
	if remaining < 0 {
		remaining = 0
	}

	switch b.state {
	case closed:
		return true, false, 0
	case open:
		if remaining > 0 {
			return false, false, remaining
		}
		// Cooldown elapsed: promote to half-open and let this one call
		// through as the probe.
		b.state = halfOpen
		b.probing = true
		return true, true, 0
	default: // halfOpen
		if b.probing {
			// A probe is already in flight -- everyone else keeps failing
			// fast rather than piling onto a provider that may still be
			// down. There is no meaningful countdown to report here: the
			// wait ends when the probe answers, not when a timer expires.
			return false, false, 0
		}
		b.probing = true
		return true, true, 0
	}
}

func (b *breaker) onFault(threshold int, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if probe {
		b.probing = false
	}
	// A failed probe re-opens immediately and restarts the cooldown --
	// don't make the caller re-count to the threshold before failing fast
	// again, the probe already answered the question.
	if b.state == halfOpen {
		b.state = open
		b.openedAt = time.Now()
		return
	}
	b.failures++
	if b.failures >= threshold {
		b.state = open
		b.openedAt = time.Now()
	}
}

func (b *breaker) onSuccess(probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if probe {
		b.probing = false
	}
	b.state = closed
	b.failures = 0
}
