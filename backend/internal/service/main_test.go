package service_test

import (
	"os"
	"testing"
)

// TestMain sets ALLOW_PRIVATE_NETWORK_TARGETS for this package's whole test
// binary. A handful of tests here exercise real outbound calls that now go
// through httpguard (WebhookSender via internal/notifier, openAIClient via
// internal/llmclient) -- see internal/notifier/webhook.go and
// internal/llmclient/llmclient.go -- against an httptest.NewServer, which
// always binds to loopback, refused by httpguard's default. httpguard's own
// blocking behavior is covered directly by internal/httpguard's tests; this
// package's tests are about service-layer behavior, not about re-proving
// the guard. Applies to both this package (service_test) and the internal
// service package's own test files -- Go links every _test.go file in a
// directory into one binary, and TestMain runs once for that whole binary
// regardless of which of the two package declarations it lives in.
func TestMain(m *testing.M) {
	os.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")
	os.Exit(m.Run())
}
