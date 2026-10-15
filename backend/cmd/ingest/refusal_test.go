package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/testutil"
)

// TestMainRefusesToStartInsecurely runs the real main() as a process: a production-like
// configuration (AUTH_MODE=jwt) with a well-known database password must exit non-zero
// with the reason, before it opens any connection. config.Config.Validate has unit tests;
// this is the proof that ingest actually calls it and stops.
func TestMainRefusesToStartInsecurely(t *testing.T) {
	if testutil.InSubprocess() {
		main()
		return
	}

	t.Run("a default database password under AUTH_MODE=jwt", func(t *testing.T) {
		code, out := testutil.RunMainInSubprocess(t, "TestMainRefusesToStartInsecurely",
			"AUTH_MODE=jwt",
			"DATABASE_URL=postgres://kuruops_app:kuruops_app@127.0.0.1:1/kuruops?sslmode=disable",
		)
		assert.Equal(t, 1, code, "must exit with status 1; output:\n%s", out)
		assert.Contains(t, out, "refusing to start")
		assert.Contains(t, out, "well-known database password")
	})

	t.Run("AUTH_MODE=dev-headers without the explicit acknowledgement", func(t *testing.T) {
		code, out := testutil.RunMainInSubprocess(t, "TestMainRefusesToStartInsecurely",
			"AUTH_MODE=dev-headers",
			"DATABASE_URL=postgres://u:a-strong-Passw0rd-xyz@127.0.0.1:1/kuruops?sslmode=disable",
		)
		assert.Equal(t, 1, code, "must exit with status 1; output:\n%s", out)
		assert.Contains(t, out, "ALLOW_INSECURE_DEV_AUTH=true")
	})
}
