package mcpclient_test

import (
	"os"
	"testing"
)

// TestMain sets ALLOW_PRIVATE_NETWORK_TARGETS for this package's whole test
// binary: Client dials through httpguard (see jsonrpc.go's New), which
// refuses loopback destinations by default, and every test in this package
// points endpoint at an httptest.NewServer, which always binds to
// 127.0.0.1. httpguard's own blocking behavior is covered directly by
// internal/httpguard's tests; this package's tests are about the MCP
// JSON-RPC protocol, not about re-proving the guard.
func TestMain(m *testing.M) {
	os.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")
	os.Exit(m.Run())
}
