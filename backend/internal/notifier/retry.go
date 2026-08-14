package notifier

import (
	"context"
	"time"
)

// retryMaxAttempts/retryBaseDelay bound how long a single escalation attempt
// can add to a worker sweep tick: worst case is 2 waits (500ms, 1s) between
// 3 attempts, ~1.5s per candidate -- small next to the 1-minute tick
// interval (see cmd/worker/main.go), even with several candidates in one
// sweep.
const (
	retryMaxAttempts = 3
	retryBaseDelay   = 500 * time.Millisecond
)

// RetryingSender wraps another Sender with a small bounded retry-with-
// backoff for transient failures (a dropped connection, a momentary 5xx from
// PagerDuty/Slack) -- exported (not a private decorator) so New/NewForPolicy
// can apply it uniformly to every channel without duplicating retry logic in
// PagerDutySender/SlackSender/WebhookSender, and so callers/tests can still
// unwrap Inner to reach the underlying Sender directly.
//
// This does not distinguish transient from permanent failures (e.g. a
// destination secret pointing at a since-revoked PagerDuty routing key) --
// every error gets the same bounded retry. That means a permanently bad
// destination wastes a couple of seconds retrying before giving up, same as
// before; the tradeoff is deliberate: previously ANY failure -- transient or
// not -- meant the responder simply never got paged with no retry safety net
// at all, which is the worse failure mode for the one mechanism whose whole
// job is telling a human something is wrong.
type RetryingSender struct {
	Inner Sender
}

func (r RetryingSender) Send(ctx context.Context, destination string, n Notification) error {
	var lastErr error
	for attempt := 0; attempt < retryMaxAttempts; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<(attempt-1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		lastErr = r.Inner.Send(ctx, destination, n)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}
