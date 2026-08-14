package notifier_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/notifier"
)

// flakySender fails the first failCount calls, then succeeds -- lets tests
// assert RetryingSender actually retries instead of just wrapping a single
// call.
type flakySender struct {
	failCount int
	calls     int
}

func (f *flakySender) Send(ctx context.Context, destination string, n notifier.Notification) error {
	f.calls++
	if f.calls <= f.failCount {
		return errors.New("transient failure")
	}
	return nil
}

func TestRetryingSender_SucceedsAfterTransientFailures(t *testing.T) {
	inner := &flakySender{failCount: 2}
	sender := notifier.RetryingSender{Inner: inner}

	err := sender.Send(t.Context(), "dest", notifier.Notification{})

	require.NoError(t, err)
	assert.Equal(t, 3, inner.calls, "expected exactly 2 failures then 1 success within the retry budget")
}

func TestRetryingSender_GivesUpAfterMaxAttempts(t *testing.T) {
	inner := &flakySender{failCount: 999}
	sender := notifier.RetryingSender{Inner: inner}

	err := sender.Send(t.Context(), "dest", notifier.Notification{})

	require.Error(t, err)
	assert.Equal(t, 3, inner.calls, "expected retries to stop at the bounded attempt count instead of retrying forever")
}

func TestRetryingSender_StopsRetryingWhenContextIsCanceled(t *testing.T) {
	inner := &flakySender{failCount: 999}
	sender := notifier.RetryingSender{Inner: inner}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// The first attempt still runs (no pre-flight ctx check), but the
	// backoff wait before attempt 2 should observe ctx.Done() and return
	// immediately instead of sleeping out the full retry budget.
	start := time.Now()
	err := sender.Send(ctx, "dest", notifier.Notification{})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 400*time.Millisecond, "expected the canceled context to short-circuit the backoff wait")
}

func TestRetryingSender_NoRetryNeededOnFirstSuccess(t *testing.T) {
	inner := &flakySender{failCount: 0}
	sender := notifier.RetryingSender{Inner: inner}

	err := sender.Send(t.Context(), "dest", notifier.Notification{})

	require.NoError(t, err)
	assert.Equal(t, 1, inner.calls)
}
