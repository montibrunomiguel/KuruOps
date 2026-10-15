package handlers_test

import (
	"os"
	"testing"
	"time"

	"github.com/kuruops/kuruops/internal/llmclient"
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
	// A fake provider that answers 5xx is retried (see llmclient.doRequest); keep
	// the waits to a millisecond so those tests do not sleep through real seconds.
	llmclient.ConfigureRetry(3, time.Millisecond)
	os.Exit(m.Run())
}
