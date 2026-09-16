package circuitbreaker_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/circuitbreaker"
)

var errBoom = errors.New("boom")

// cfg is deliberately tiny on both axes so the tests exercise real timing
// (an actually-elapsed cooldown) without sleeping long enough to slow the
// suite down.
func cfg() circuitbreaker.Config {
	return circuitbreaker.Config{Threshold: 3, Cooldown: 50 * time.Millisecond}
}

func TestRegistry_OpensAfterThresholdFaults(t *testing.T) {
	r := circuitbreaker.NewRegistry(cfg())
	calls := 0
	fail := func() error { calls++; return circuitbreaker.Fault(errBoom) }

	for i := 0; i < 3; i++ {
		err := r.Do("provider-a", fail)
		require.ErrorIs(t, err, errBoom, "call %d should still reach the provider", i+1)
		require.False(t, errors.Is(err, circuitbreaker.ErrOpen))
	}
	assert.Equal(t, 3, calls)

	t.Run("the next call fails fast without calling through", func(t *testing.T) {
		err := r.Do("provider-a", fail)
		assert.ErrorIs(t, err, circuitbreaker.ErrOpen)
		assert.Equal(t, 3, calls, "fn must not be called while the breaker is open")
	})

	t.Run("the error names the key and the wait", func(t *testing.T) {
		err := r.Do("provider-a", fail)
		assert.ErrorContains(t, err, "provider-a")
		assert.ErrorContains(t, err, "next attempt in")
	})
}

// TestRegistry_OnlyFaultsCount is the finding this design exists to avoid:
// a wrong API key returns 401 on every single call, so counting it would
// open the breaker permanently and replace a precise, fixable error with a
// generic "circuit breaker is open".
func TestRegistry_OnlyFaultsCount(t *testing.T) {
	r := circuitbreaker.NewRegistry(cfg())
	calls := 0
	clientErr := func() error { calls++; return errBoom } // not marked as a Fault

	for i := 0; i < 10; i++ {
		err := r.Do("provider-a", clientErr)
		require.ErrorIs(t, err, errBoom)
		require.False(t, errors.Is(err, circuitbreaker.ErrOpen), "an unmarked error must never trip the breaker")
	}
	assert.Equal(t, 10, calls, "every call must still reach the provider")
}

func TestRegistry_SuccessResetsTheFailureCount(t *testing.T) {
	r := circuitbreaker.NewRegistry(cfg())
	fail := func() error { return circuitbreaker.Fault(errBoom) }
	ok := func() error { return nil }

	// Two faults, a success, then two more faults: five failures in total
	// but never three consecutively, so the breaker stays closed.
	require.ErrorIs(t, r.Do("p", fail), errBoom)
	require.ErrorIs(t, r.Do("p", fail), errBoom)
	require.NoError(t, r.Do("p", ok))
	require.ErrorIs(t, r.Do("p", fail), errBoom)
	require.ErrorIs(t, r.Do("p", fail), errBoom)

	err := r.Do("p", fail)
	assert.ErrorIs(t, err, errBoom)
	assert.False(t, errors.Is(err, circuitbreaker.ErrOpen),
		"the threshold counts consecutive faults, so an intervening success must reset it")
}

func TestRegistry_KeysAreIndependent(t *testing.T) {
	// The reason breakers are keyed at all: one dead provider must not fail
	// calls to a healthy one.
	r := circuitbreaker.NewRegistry(cfg())
	fail := func() error { return circuitbreaker.Fault(errBoom) }

	for i := 0; i < 3; i++ {
		require.ErrorIs(t, r.Do("broken", fail), errBoom)
	}
	require.ErrorIs(t, r.Do("broken", fail), circuitbreaker.ErrOpen)

	healthy := 0
	require.NoError(t, r.Do("healthy", func() error { healthy++; return nil }))
	assert.Equal(t, 1, healthy)
}

func TestRegistry_HalfOpenRecovery(t *testing.T) {
	r := circuitbreaker.NewRegistry(cfg())
	fail := func() error { return circuitbreaker.Fault(errBoom) }

	trip := func(t *testing.T) {
		t.Helper()
		for i := 0; i < 3; i++ {
			require.ErrorIs(t, r.Do("p", fail), errBoom)
		}
		require.ErrorIs(t, r.Do("p", fail), circuitbreaker.ErrOpen)
	}

	t.Run("a successful probe closes the breaker", func(t *testing.T) {
		trip(t)
		time.Sleep(60 * time.Millisecond)

		require.NoError(t, r.Do("p", func() error { return nil }), "the probe should be let through")

		// Closed again: a normal call goes through, and a single fault no
		// longer trips it (the counter reset).
		called := false
		require.ErrorIs(t, r.Do("p", func() error { called = true; return circuitbreaker.Fault(errBoom) }), errBoom)
		assert.True(t, called)
	})

	t.Run("a failed probe re-opens immediately, without re-counting to the threshold", func(t *testing.T) {
		r := circuitbreaker.NewRegistry(cfg())
		for i := 0; i < 3; i++ {
			require.ErrorIs(t, r.Do("q", fail), errBoom)
		}
		time.Sleep(60 * time.Millisecond)

		require.ErrorIs(t, r.Do("q", fail), errBoom, "the probe itself reaches the provider")

		calls := 0
		err := r.Do("q", func() error { calls++; return nil })
		assert.ErrorIs(t, err, circuitbreaker.ErrOpen,
			"one failed probe is answer enough -- don't make the caller re-count to the threshold")
		assert.Zero(t, calls)
	})
}

// TestRegistry_OnlyOneProbeInFlight guards the thundering-herd case: when
// the cooldown expires on a busy system, a recovering provider must get one
// request, not every queued one at once.
func TestRegistry_OnlyOneProbeInFlight(t *testing.T) {
	r := circuitbreaker.NewRegistry(cfg())
	fail := func() error { return circuitbreaker.Fault(errBoom) }
	for i := 0; i < 3; i++ {
		require.ErrorIs(t, r.Do("p", fail), errBoom)
	}
	time.Sleep(60 * time.Millisecond)

	var inFlight atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Do("p", func() error {
				inFlight.Add(1)
				<-release // hold the probe open so the others race against it
				return nil
			})
		}()
	}

	assert.Eventually(t, func() bool { return inFlight.Load() == 1 }, time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond) // give any extra probe a chance to slip through
	assert.EqualValues(t, 1, inFlight.Load(), "exactly one probe may reach a recovering provider")
	close(release)
	wg.Wait()
}

func TestRegistry_DisabledByZeroConfig(t *testing.T) {
	// The zero Config is how the breaker is turned off -- leaving the env
	// vars unset must restore the exact pre-breaker behaviour.
	for name, c := range map[string]circuitbreaker.Config{
		"zero value":   {},
		"no threshold": {Cooldown: time.Second},
		"no cooldown":  {Threshold: 3},
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, c.Enabled())
			r := circuitbreaker.NewRegistry(c)
			calls := 0
			for i := 0; i < 10; i++ {
				err := r.Do("p", func() error { calls++; return circuitbreaker.Fault(errBoom) })
				require.ErrorIs(t, err, errBoom)
				require.False(t, errors.Is(err, circuitbreaker.ErrOpen))
			}
			assert.Equal(t, 10, calls)
		})
	}
}

func TestFault(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		assert.NoError(t, circuitbreaker.Fault(nil))
		assert.False(t, circuitbreaker.IsFault(nil))
	})

	t.Run("the original error stays reachable through errors.Is", func(t *testing.T) {
		// Callers still need to see the provider's actual error -- the mark
		// must not swallow it.
		err := circuitbreaker.Fault(fmt.Errorf("wrapped: %w", errBoom))
		assert.True(t, circuitbreaker.IsFault(err))
		assert.ErrorIs(t, err, errBoom)
		assert.ErrorContains(t, err, "boom")
	})

	t.Run("an unmarked error is not a fault", func(t *testing.T) {
		assert.False(t, circuitbreaker.IsFault(errBoom))
	})

	t.Run("a mark survives further wrapping", func(t *testing.T) {
		err := fmt.Errorf("context: %w", circuitbreaker.Fault(errBoom))
		assert.True(t, circuitbreaker.IsFault(err))
	})
}
