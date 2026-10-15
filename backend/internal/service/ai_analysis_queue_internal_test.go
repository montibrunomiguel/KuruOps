package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/circuitbreaker"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/llmclient"
)

// classify is the decision table that says what happens to an alert's analysis
// when it fails. It is pure, so it is tested directly and exhaustively: the
// difference between "retry in a minute" and "give up" is the difference between
// an alert that gets analysed and one that never does.
func TestAIAnalysisQueue_Classify(t *testing.T) {
	q := &AIAnalysisQueue{cfg: AIAnalysisQueueConfig{Backoff: []time.Duration{time.Minute, 5 * time.Minute, time.Hour}}.withDefaults()}
	job := func(attempt, max int) domain.AIAnalysisJob {
		return domain.AIAnalysisJob{Attempts: attempt, MaxAttempts: max}
	}

	tests := []struct {
		name string
		err  error
		job  domain.AIAnalysisJob
		kind outcomeKind
	}{
		{"success", nil, job(1, 5), outcomeSucceeded},

		// Nothing to do, ever: normal states, not failures.
		{"auto-analysis switched off", ErrAutoAnalysisDisabled, job(1, 5), outcomeSkipped},
		{"no provider configured", ErrNoLLMProvider, job(1, 5), outcomeSkipped},
		{"the alert was deleted", &subjectNotFoundError{"alert x not found"}, job(1, 5), outcomeSkipped},
		{"a skip stays a skip when wrapped", fmt.Errorf("prepare: %w", ErrAutoAnalysisDisabled), job(1, 5), outcomeSkipped},

		// Will fail the same way every time: do not burn attempts on it.
		{"rejected API key", fmt.Errorf("llm analysis: %w", &llmclient.ProviderError{Status: 401}), job(1, 5), outcomeFailed},
		{"forbidden", &llmclient.ProviderError{Status: 403}, job(1, 5), outcomeFailed},
		{"wrong URL or model", &llmclient.ProviderError{Status: 404}, job(1, 5), outcomeFailed},
		{"bad request", &llmclient.ProviderError{Status: 400}, job(1, 5), outcomeFailed},
		{"blocked destination", &llmclient.TransportError{Err: errors.New("httpguard: 10.0.0.1 is a private address")}, job(1, 5), outcomeFailed},

		// Worth trying again later.
		{"provider overloaded", fmt.Errorf("llm analysis: %w", &llmclient.ProviderError{Status: 503}), job(1, 5), outcomeRetry},
		{"provider rate limiting", &llmclient.ProviderError{Status: 429}, job(1, 5), outcomeRetry},
		{"provider 500", &llmclient.ProviderError{Status: 500}, job(2, 5), outcomeRetry},
		{"circuit breaker open", fmt.Errorf("%w for host", circuitbreaker.ErrOpen), job(1, 5), outcomeRetry},
		{"another analysis still running", ErrAnalysisInProgress, job(1, 5), outcomeRetry},
		{"a timeout", &llmclient.TransportError{Err: context.DeadlineExceeded}, job(1, 5), outcomeRetry},
		{"an unrecognised error leans to retry", errors.New("connection reset by peer"), job(1, 5), outcomeRetry},

		// Out of attempts.
		{"retryable but out of attempts", &llmclient.ProviderError{Status: 503}, job(5, 5), outcomeFailed},
		{"unrecognised and out of attempts", errors.New("boom"), job(3, 3), outcomeFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.kind, q.classify(tc.job, tc.err).kind)
		})
	}

	t.Run("the failure reason is kept, so 'why was this not analysed' has an answer", func(t *testing.T) {
		got := q.classify(job(1, 5), &llmclient.ProviderError{Status: 401, Message: "the AI provider rejected the credentials (401)"})
		assert.Contains(t, got.reason, "rejected the credentials")
		got = q.classify(job(5, 5), &llmclient.ProviderError{Status: 503, Message: "temporarily unavailable"})
		assert.Contains(t, got.reason, "gave up after 5 attempts")
		assert.Contains(t, got.reason, "temporarily unavailable")
	})
}

func TestAIAnalysisQueue_Backoff(t *testing.T) {
	q := &AIAnalysisQueue{cfg: AIAnalysisQueueConfig{Backoff: []time.Duration{time.Minute, 5 * time.Minute, time.Hour}}.withDefaults()}

	within := func(t *testing.T, got, nominal time.Duration) {
		t.Helper()
		assert.GreaterOrEqual(t, got, time.Duration(float64(nominal)*0.8)-time.Millisecond)
		assert.LessOrEqual(t, got, time.Duration(float64(nominal)*1.2)+time.Millisecond)
	}
	for i := 0; i < 200; i++ {
		within(t, q.backoff(1), time.Minute)
		within(t, q.backoff(2), 5*time.Minute)
		within(t, q.backoff(3), time.Hour)
		within(t, q.backoff(4), time.Hour) // the last entry repeats
		within(t, q.backoff(50), time.Hour)
		within(t, q.backoff(0), time.Minute) // never indexes below the table
	}

	t.Run("jitter actually varies", func(t *testing.T) {
		seen := map[time.Duration]bool{}
		for i := 0; i < 50; i++ {
			seen[q.backoff(1)] = true
		}
		assert.Greater(t, len(seen), 10, "jobs that failed together must not all come due at the same instant")
	})

	t.Run("the default schedule rides out about an hour and a half", func(t *testing.T) {
		var total time.Duration
		for _, d := range DefaultAnalysisBackoff[:DefaultAnalysisJobMaxAttempts-1] {
			total += d
		}
		assert.Equal(t, time.Minute+5*time.Minute+15*time.Minute+time.Hour, total)
	})
}

func TestAIAnalysisQueueConfig_Defaults(t *testing.T) {
	c := AIAnalysisQueueConfig{}.withDefaults()
	assert.Equal(t, 5, c.Concurrency)
	assert.Equal(t, 5*time.Second, c.PollInterval)
	assert.Equal(t, 2*time.Minute, c.Lease)
	assert.Equal(t, 40*time.Second, c.HeartbeatEvery, "a third of the lease, so two beats can be missed before it lapses")
	assert.Less(t, c.HeartbeatEvery, c.Lease)

	custom := AIAnalysisQueueConfig{Concurrency: 2, Lease: 9 * time.Second}.withDefaults()
	assert.Equal(t, 2, custom.Concurrency)
	assert.Equal(t, 3*time.Second, custom.HeartbeatEvery, "follows a custom lease")
}
