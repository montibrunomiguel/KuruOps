package httpguard_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/httpguard"
)

// TestPreflightURL covers the save-time advisory check. It exists so an
// operator typing a destination the dial-time guard will always refuse finds
// out in the settings form rather than during the incident the destination
// was configured for.
func TestPreflightURL(t *testing.T) {
	t.Run("blocked literal addresses are refused", func(t *testing.T) {
		for _, raw := range []string{
			"http://169.254.169.254/latest/meta-data/", // cloud metadata, the classic SSRF pivot
			"http://127.0.0.1:5432/",
			"http://localhost:8080/hook", // resolves to loopback
			"https://10.0.0.5/notify",
			"http://192.168.1.10/notify",
			"http://172.16.4.4/notify",
			"http://0.0.0.0/",
		} {
			assert.Error(t, httpguard.PreflightURL(raw), "should refuse %q", raw)
		}
	})

	t.Run("a public address passes", func(t *testing.T) {
		for _, raw := range []string{
			"https://hooks.example.com/services/abc",
			"http://93.184.216.34/notify",
		} {
			assert.NoError(t, httpguard.PreflightURL(raw), "should accept %q", raw)
		}
	})

	t.Run("a malformed or non-HTTP URL is refused", func(t *testing.T) {
		for _, raw := range []string{"", "not a url", "ftp://files.example.com/x", "file:///etc/passwd"} {
			assert.Error(t, httpguard.PreflightURL(raw), "should refuse %q", raw)
		}
	})

	t.Run("a name that does not resolve is allowed through", func(t *testing.T) {
		// Deliberately permissive: an unresolvable name may be internal DNS
		// that resolves fine from the server, and refusing to save on a
		// failed lookup would block legitimate configuration. The dial-time
		// guard still has the last word.
		assert.NoError(t, httpguard.PreflightURL("https://nonexistent.invalid/hook"))
	})

	t.Run("the on-prem escape hatch disables the check", func(t *testing.T) {
		t.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "true")
		require.NoError(t, httpguard.PreflightURL("http://10.0.0.5/notify"),
			"a genuine on-prem deployment must still be able to configure a private destination")
	})

	t.Run("the escape hatch only counts when it is exactly true", func(t *testing.T) {
		t.Setenv("ALLOW_PRIVATE_NETWORK_TARGETS", "yes")
		assert.Error(t, httpguard.PreflightURL("http://10.0.0.5/notify"))
	})
}
