package handlers_test

import (
	"os"
	"testing"
	"time"

	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/service"
)

// TestMain sets ALLOW_PRIVATE_NETWORK_TARGETS for this package's whole test
// binary. A handful of tests here exercise real outbound calls that now go
// through httpguard (WebhookSender via internal/notifier, openAIClient via
// internal/llmclient) -- see internal/notifier/webhook.go and
// internal/llmclient/llmclient.go -- against an httptest.NewServer, which
// always binds to loopback, refused by httpguard's default. httpguard's own
// blocking behavior is covered directly by internal/httpguard's tests; this
// package's tests are about HTTP handler behavior, not about re-proving the
// guard.
func TestMain(m *testing.M) {
	os.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")
	os.Exit(m.Run())
}

// fastLLM gives an AIAnalysisService a provider-call retry policy measured in
// milliseconds: a fake provider that answers 5xx is retried, and these tests should
// not sleep through real seconds. It is per service, so it leaks into nothing else.
func fastLLM() service.AIAnalysisOption {
	return service.WithLLMClientFactory(llmclient.NewFactory(llmclient.Options{
		Retry: llmclient.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond},
	}))
}
