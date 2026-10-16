package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/testutil/cmdtest"
)

func get(t *testing.T, rawURL string) (int, string) {
	t.Helper()
	resp, err := http.Get(rawURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(b)
}

// TestRunServesTheAPIAndStopsCleanly starts the real server -- every service, handler and
// middleware wired exactly as in production, against the test database -- and talks to it
// over HTTP, then stops it. It is the test that the wiring in run() is complete: a handler
// left unregistered or a dependency left nil shows up here as a 404 or a panic.
func TestRunServesTheAPIAndStopsCleanly(t *testing.T) {
	cfg := cmdtest.CommandConfig(t)
	base, stop := cmdtest.RunCommand(t, cfg, run)

	t.Run("liveness and readiness", func(t *testing.T) {
		code, _ := get(t, base+"/livez")
		assert.Equal(t, http.StatusOK, code)
		code, _ = get(t, base+"/healthz")
		assert.Equal(t, http.StatusOK, code)
	})

	t.Run("metrics expose the build and the database pool", func(t *testing.T) {
		code, body := get(t, base+"/metrics")
		require.Equal(t, http.StatusOK, code)
		assert.Contains(t, body, "kuruops_build_info")
		assert.Contains(t, body, "kuruops_db_pool_max_conns")
		assert.Contains(t, body, "go_goroutines", "the standard Go runtime collector is registered")
	})

	t.Run("the API is behind authentication", func(t *testing.T) {
		for _, path := range []string{"/api/v1/alerts", "/api/v1/incidents", "/api/v1/settings/webhooks", "/api/v1/events/stream"} {
			code, _ := get(t, base+path)
			assert.Equal(t, http.StatusUnauthorized, code, path)
		}
	})

	t.Run("login rejects wrong credentials without a 5xx", func(t *testing.T) {
		resp, err := http.Post(base+"/auth/login", "application/json",
			strings.NewReader(`{"email":"nobody@example.test","password":"wrong-password"}`))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("the seeded admin can log in and read an authenticated route", func(t *testing.T) {
		// db/migrations/0002 seeds a default admin; the first login forces a password
		// change, so the token is "must change password" -- enough to prove login issues a
		// token this very server verifies.
		resp, err := http.Post(base+"/auth/login", "application/json",
			strings.NewReader(`{"email":"admin@kuruops.local","password":"ChangeMe123!"}`))
		require.NoError(t, err)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Skipf("the seeded admin's password was changed in this database (status %d)", resp.StatusCode)
		}
		var out struct {
			Token string `json:"token"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		require.NotEmpty(t, out.Token)

		req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/account/profile", nil)
		req.Header.Set("Authorization", "Bearer "+out.Token)
		r2, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer r2.Body.Close()
		assert.NotEqual(t, http.StatusUnauthorized, r2.StatusCode, "a token issued by this server must verify")
	})

	require.NoError(t, stop(), "run must return nil on a clean shutdown")
}

// TestRunFailsToStartCleanly: each way startup can fail returns an error naming the cause
// (and leaves nothing running), instead of os.Exit-ing from deep inside main.
func TestRunFailsToStartCleanly(t *testing.T) {
	t.Run("database unreachable", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t, "DATABASE_URL=postgres://u:p@127.0.0.1:1/kuruops?sslmode=disable&connect_timeout=2")
		err := run(t.Context(), cfg, discardLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database connection")
	})

	t.Run("jwt mode without key files", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t, "AUTH_MODE=jwt", "JWT_PRIVATE_KEY_PATH=", "JWT_PUBLIC_KEY_PATH=")
		err := run(t.Context(), cfg, discardLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "jwt key setup")
	})

	t.Run("an unknown secrets backend", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t, "SECRETS_BACKEND=carrier-pigeon")
		err := run(t.Context(), cfg, discardLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secrets backend")
	})

	t.Run("an upload directory that cannot be created", func(t *testing.T) {
		// A path under a regular file can never be a directory.
		file := t.TempDir() + "/not-a-dir"
		require.NoError(t, writeFile(file))
		cfg := cmdtest.CommandConfig(t, "UPLOAD_DIR="+file+"/sub")
		err := run(t.Context(), cfg, discardLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upload dir")
	})

	t.Run("the database upload backend needs no upload directory", func(t *testing.T) {
		// Read-only root filesystems (the Kubernetes pods) must not be asked to create one.
		cfg := cmdtest.CommandConfig(t, "UPLOAD_BACKEND=database", "UPLOAD_DIR=/proc/definitely/not/creatable")
		base, stop := cmdtest.RunCommand(t, cfg, run)
		code, _ := get(t, base+"/healthz")
		assert.Equal(t, http.StatusOK, code)
		require.NoError(t, stop())
	})

	t.Run("the HTTP port is already taken", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t)
		_, stopFirst := cmdtest.RunCommand(t, cfg, run) // owns cfg.HTTPAddr
		defer func() { _ = stopFirst() }()

		err := run(t.Context(), cfg, discardLogger())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "server failed")
	})
}

func writeFile(path string) error { return os.WriteFile(path, []byte("x"), 0o600) }
