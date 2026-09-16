package llmclient

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProviderError covers what an analyst is shown when the AI provider
// refuses. These messages are rendered verbatim in the analysis panel, so
// each one has to name the thing to go and change -- the previous behaviour
// put the provider's whole response body on screen, which for a Gemini
// outage meant a multi-line JSON blob and for a misconfigured Base URL meant
// a bare "llm provider returned 404:" with nothing after the colon.
func TestProviderError(t *testing.T) {
	gemini503 := []byte(`[{
  "error": {
    "code": 503,
    "message": "This model is currently experiencing high demand. Spikes in demand are usually temporary.",
    "status": "UNAVAILABLE"
  }
}]`)

	t.Run("a provider outage reads as transient, not as a bug to report", func(t *testing.T) {
		err := providerError(http.StatusServiceUnavailable, gemini503)
		assert.ErrorContains(t, err, "temporarily unavailable")
		assert.ErrorContains(t, err, "try analysing again")
		assert.NotContains(t, err.Error(), "UNAVAILABLE",
			"the provider's own JSON must not reach the panel")
	})

	t.Run("a 404 points at the setting that is actually wrong", func(t *testing.T) {
		// The case that cost real time here: a Base URL with the
		// /chat/completions suffix left on it produced seven failed runs
		// whose error said only "llm provider returned 404:".
		err := providerError(http.StatusNotFound, []byte(""))
		assert.ErrorContains(t, err, "Base URL")
		assert.ErrorContains(t, err, "Settings -> AI Integration")
	})

	t.Run("a rejected key says so, and where to fix it", func(t *testing.T) {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			err := providerError(status, []byte(`{"error":"invalid x-api-key"}`))
			assert.ErrorContains(t, err, "API key")
			assert.ErrorContains(t, err, "Settings -> AI Integration")
		}
	})

	t.Run("rate limiting is distinguished from an outage", func(t *testing.T) {
		err := providerError(http.StatusTooManyRequests, []byte("slow down"))
		assert.ErrorContains(t, err, "rate-limiting")
	})

	t.Run("an unrecognised status still carries the body, truncated", func(t *testing.T) {
		// For anything not mapped above the raw body is the only clue, so it
		// is kept -- but bounded, since some providers answer with kilobytes
		// of HTML.
		huge := []byte(strings.Repeat("<html>padding</html>", 500))
		err := providerError(http.StatusTeapot, huge)
		assert.ErrorContains(t, err, "418")
		assert.ErrorContains(t, err, "truncated")
		assert.Less(t, len(err.Error()), 500, "an error banner has to stay readable")
	})

	t.Run("a short unrecognised body is passed through whole", func(t *testing.T) {
		err := providerError(http.StatusBadRequest, []byte("model not supported"))
		assert.ErrorContains(t, err, "model not supported")
		assert.NotContains(t, err.Error(), "truncated")
	})
}
