package llmclient_test

import (
	"os"
	"testing"
)

// TestMain sets ALLOW_PRIVATE_NETWORK_TARGETS for this package's whole test
// binary: openAIClient dials through httpguard (see llmclient.go's
// doRequest), which refuses loopback destinations by default, and this
// package's tests point baseURL at httptest.NewServer, which always binds
// to 127.0.0.1. httpguard's own blocking behavior is covered directly by
// internal/httpguard's tests; this package's tests are about the LLM
// client's own request/response handling, not about re-proving the guard.
// Applies to both this package (llmclient_test) and the internal
// llmclient package's own test files -- Go links every _test.go file in a
// directory into one binary, and TestMain runs once for that whole binary
// regardless of which of the two package declarations it lives in.
func TestMain(m *testing.M) {
	os.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")
	os.Exit(m.Run())
}
