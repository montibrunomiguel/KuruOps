package httpguard_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/httpguard"
)

// withAllowPrivateNetworkTargets temporarily sets/unsets
// ALLOW_PRIVATE_NETWORK_TARGETS -- httpguard reads it once via a
// package-level var initialized at import time, so re-exercising both
// branches within one test binary run means these tests can't just set the
// env var; they must run in a subprocess per branch. Simpler: these tests
// instead cover the always-blocked default (the value is unset for the
// whole test binary) and separately unit-test isDisallowed-equivalent IP
// classification indirectly through NewClient's actual dial behavior.
func TestNewClient_BlocksLoopbackByDefault(t *testing.T) {
	if os.Getenv("ALLOW_PRIVATE_NETWORK_TARGETS") == "true" {
		t.Skip("ALLOW_PRIVATE_NETWORK_TARGETS=true is set in this environment -- this test asserts the opposite default")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := httpguard.NewClient(2 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	require.Error(t, err, "httptest.NewServer listens on 127.0.0.1 -- a loopback address must be refused")
	assert.ErrorContains(t, err, "httpguard")
	assert.ErrorContains(t, err, "loopback/link-local/private addresses are blocked")
}

func TestNewClient_BlocksLinkLocal(t *testing.T) {
	if os.Getenv("ALLOW_PRIVATE_NETWORK_TARGETS") == "true" {
		t.Skip("ALLOW_PRIVATE_NETWORK_TARGETS=true is set in this environment -- this test asserts the opposite default")
	}

	client := httpguard.NewClient(2 * time.Second)
	// 169.254.169.254 is the well-known cloud metadata endpoint (AWS/GCP/
	// Azure) -- the single most consequential SSRF target this guard
	// exists to close off. No real listener is needed: dial refusal must
	// happen before any connection attempt, so this must fail fast with
	// httpguard's own error, not a connection-refused/timeout error.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	require.Error(t, err)
	assert.ErrorContains(t, err, "httpguard")
}

func TestNewClient_AllowsPublicAddresses(t *testing.T) {
	// Loopback IS the only address a local httptest.Server can bind to, so
	// this test can't spin up a real "public" listener -- instead it
	// verifies the guard resolves and evaluates a real public hostname
	// without blocking it, distinguishing "refused by httpguard" from any
	// other failure mode by asserting the error (if any, e.g. no network
	// access in a sandboxed CI runner) is NOT an httpguard refusal.
	client := httpguard.NewClient(2 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://1.1.1.1/", nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	if err != nil {
		assert.NotContains(t, err.Error(), "httpguard", "a public IP must not be refused by the guard itself, whatever else may fail in a sandboxed test runner")
	}
}

func TestNewClient_RefusesWhenHostnameDoesNotResolve(t *testing.T) {
	client := httpguard.NewClient(2 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://this-hostname-should-never-resolve.invalid/", nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	require.Error(t, err)
}

// dialAddr is a small helper confirming the transport's DialContext is
// reached at all (as opposed to failing earlier, e.g. on URL parsing) --
// used to sanity-check the loopback test above isn't accidentally passing
// for an unrelated reason.
func dialAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().String()
}

func TestNewClient_BlocksRawLoopbackIP(t *testing.T) {
	if os.Getenv("ALLOW_PRIVATE_NETWORK_TARGETS") == "true" {
		t.Skip("ALLOW_PRIVATE_NETWORK_TARGETS=true is set in this environment -- this test asserts the opposite default")
	}
	addr := dialAddr(t)

	client := httpguard.NewClient(2 * time.Second)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/", nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	require.Error(t, err)
	assert.ErrorContains(t, err, "httpguard")
}
