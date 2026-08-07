package ingest_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/ingest"
)

func TestCrowdStrikeNormalizer_Normalize(t *testing.T) {
	n := ingest.NewCrowdStrikeNormalizer()

	t.Run("full valid envelope with SeverityName", func(t *testing.T) {
		raw := []byte(`{
			"event": {
				"DetectId": "ldt:abc:123",
				"DetectName": "Suspicious PowerShell execution",
				"Severity": 70,
				"SeverityName": "High",
				"ComputerName": "WORKSTATION-01",
				"LocalIP": "10.0.0.20",
				"Tactic": "Execution",
				"Technique": "PowerShell"
			}
		}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		assert.Equal(t, "Suspicious PowerShell execution", got.Title)
		assert.Equal(t, domain.SeverityHigh, got.Severity)
		require.NotNil(t, got.ExternalID)
		assert.Equal(t, "ldt:abc:123", *got.ExternalID)
		require.NotNil(t, got.Asset)
		assert.Equal(t, "WORKSTATION-01", *got.Asset)
		require.NotNil(t, got.SrcIP)
		assert.Equal(t, "10.0.0.20", *got.SrcIP)
		assert.Equal(t, []string{"Execution", "PowerShell"}, got.Tags)
	})

	t.Run("falls back to numeric Severity when SeverityName is absent", func(t *testing.T) {
		cases := []struct {
			score int
			want  domain.Severity
		}{
			{95, domain.SeverityCritical}, {90, domain.SeverityCritical},
			{89, domain.SeverityHigh}, {70, domain.SeverityHigh},
			{69, domain.SeverityMedium}, {40, domain.SeverityMedium},
			{39, domain.SeverityLow}, {20, domain.SeverityLow},
			{10, domain.SeverityInformational},
		}
		for _, c := range cases {
			raw := []byte(`{"event": {"DetectName": "t", "Severity": ` + strconv.Itoa(c.score) + `}}`)
			got, err := n.Normalize(raw)
			require.NoError(t, err)
			assert.Equal(t, c.want, got.Severity, "score %d", c.score)
		}
	})

	t.Run("missing event.DetectName is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(`{"event": {"Severity": 50}}`))
		assert.ErrorContains(t, err, "DetectName")
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(`{not json`))
		assert.Error(t, err)
	})
}
