package ingest_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/ingest"
)

// TestGenericNormalizer_SeverityAliases covers the generic webhook path's
// severity vocabulary. Matching used to be exact and case-sensitive, so a
// source sending "High" -- which most SIEM and EDR products do -- had every
// alert rejected at the door with a 400.
func TestGenericNormalizer_SeverityAliases(t *testing.T) {
	n := ingest.NewGenericNormalizer()

	normalize := func(t *testing.T, severity string) (ingest.NormalizedAlert, error) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"title": "t", "severity": severity})
		require.NoError(t, err)
		return n.Normalize(body)
	}

	t.Run("case and surrounding whitespace are tolerated", func(t *testing.T) {
		for _, raw := range []string{"high", "High", "HIGH", "  High  ", "hIgH"} {
			got, err := normalize(t, raw)
			require.NoError(t, err, "severity %q", raw)
			assert.Equal(t, domain.SeverityHigh, got.Severity, "severity %q", raw)
		}
	})

	t.Run("the spellings other products actually emit are mapped", func(t *testing.T) {
		for raw, want := range map[string]domain.Severity{
			"crit":        domain.SeverityCritical,
			"fatal":       domain.SeverityCritical,
			"error":       domain.SeverityHigh,
			"warn":        domain.SeverityMedium,
			"warning":     domain.SeverityMedium,
			"minor":       domain.SeverityLow,
			"info":        domain.SeverityInformational,
			"information": domain.SeverityInformational,
			"notice":      domain.SeverityInformational,
		} {
			got, err := normalize(t, raw)
			require.NoError(t, err, "severity %q", raw)
			assert.Equal(t, want, got.Severity, "severity %q", raw)
		}
	})

	t.Run("every canonical value still round-trips to itself", func(t *testing.T) {
		for _, want := range []domain.Severity{
			domain.SeverityCritical, domain.SeverityHigh, domain.SeverityMedium,
			domain.SeverityLow, domain.SeverityInformational,
		} {
			got, err := normalize(t, string(want))
			require.NoError(t, err)
			assert.Equal(t, want, got.Severity)
		}
	})

	t.Run("an unrecognised value is still rejected, and the error names the accepted ones", func(t *testing.T) {
		// Guessing a severity would be worse than refusing: an alert
		// silently filed at the wrong priority is harder to notice than one
		// that never arrived.
		_, err := normalize(t, "catastrophic")
		require.Error(t, err)
		assert.ErrorContains(t, err, "catastrophic")
		for _, want := range []string{"critical", "high", "medium", "low", "informational"} {
			assert.ErrorContains(t, err, want, "the error should tell the sender what is accepted")
		}
	})

	t.Run("an empty severity is rejected rather than defaulted", func(t *testing.T) {
		_, err := normalize(t, "")
		require.Error(t, err)
	})

	t.Run("aliases never collide", func(t *testing.T) {
		// A regression guard for the map itself: two spellings resolving to
		// different severities is fine, but the same spelling appearing
		// twice would silently drop one.
		seen := map[string]bool{}
		for _, raw := range []string{"critical", "crit", "fatal", "sev1", "high", "error", "err", "sev2",
			"medium", "moderate", "warning", "warn", "sev3", "low", "minor", "sev4",
			"informational", "info", "information", "notice", "debug"} {
			require.False(t, seen[raw], fmt.Sprintf("duplicate alias %q", raw))
			seen[raw] = true
			_, err := normalize(t, raw)
			require.NoError(t, err, "alias %q should resolve", raw)
		}
	})
}
