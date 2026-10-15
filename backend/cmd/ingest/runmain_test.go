package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/testutil/cmdtest"
)

// TestRunMain drives the command's whole startup path in-process: runMain is main minus the
// os.Exit, so the exit status is an ordinary return value.
func TestRunMain(t *testing.T) {
	t.Run("an insecure configuration is refused with status 1 and the reason", func(t *testing.T) {
		t.Setenv("AUTH_MODE", "jwt")
		t.Setenv("DATABASE_URL", "postgres://kuruops_app:kuruops_app@127.0.0.1:1/kuruops?sslmode=disable")
		var out bytes.Buffer
		assert.Equal(t, 1, runMain(t.Context(), &out))
		assert.Contains(t, out.String(), "startup refused")
		assert.Contains(t, out.String(), "well-known database password")
	})

	t.Run("a valid configuration starts, serves and stops with status 0", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t)
		assert.Equal(t, 0, cmdtest.RunMainUntilHealthy(t, cfg, runMain))
	})

	t.Run("a failure to start is status 1", func(t *testing.T) {
		cfg := cmdtest.CommandConfig(t)
		t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/kuruops?sslmode=disable&connect_timeout=2")
		_ = cfg
		var out bytes.Buffer
		assert.Equal(t, 1, runMain(t.Context(), &out))
		assert.Contains(t, out.String(), "ingest stopped")
	})
}
