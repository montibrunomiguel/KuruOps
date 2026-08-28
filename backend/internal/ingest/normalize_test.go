package ingest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/ingest"
)

func TestGenericNormalizer_Normalize(t *testing.T) {
	n := ingest.NewGenericNormalizer()

	t.Run("full valid envelope", func(t *testing.T) {
		raw := []byte(`{
			"title": "Suspicious login",
			"severity": "high",
			"external_id": "ext-1",
			"rule_id": "rule-42",
			"asset": "host-01",
			"src_ip": "10.0.0.5",
			"tags": ["phishing", "vpn"]
		}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		assert.Equal(t, "Suspicious login", got.Title)
		assert.Equal(t, domain.SeverityHigh, got.Severity)
		require.NotNil(t, got.ExternalID)
		assert.Equal(t, "ext-1", *got.ExternalID)
		require.NotNil(t, got.RuleID)
		assert.Equal(t, "rule-42", *got.RuleID)
		require.NotNil(t, got.Asset)
		assert.Equal(t, "host-01", *got.Asset)
		require.NotNil(t, got.SrcIP)
		assert.Equal(t, "10.0.0.5", *got.SrcIP)
		assert.Equal(t, []string{"phishing", "vpn"}, got.Tags)
	})

	t.Run("minimal envelope with only required fields", func(t *testing.T) {
		raw := []byte(`{"title": "Minimal alert", "severity": "low"}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		assert.Equal(t, "Minimal alert", got.Title)
		assert.Equal(t, domain.SeverityLow, got.Severity)
		assert.Nil(t, got.ExternalID)
		assert.Nil(t, got.RuleID)
		assert.Nil(t, got.Asset)
		assert.Nil(t, got.SrcIP)
		assert.Empty(t, got.Tags)
	})

	t.Run("every known severity is accepted", func(t *testing.T) {
		for _, sev := range []domain.Severity{
			domain.SeverityCritical, domain.SeverityHigh, domain.SeverityMedium,
			domain.SeverityLow, domain.SeverityInformational,
		} {
			raw := []byte(`{"title": "t", "severity": "` + string(sev) + `"}`)
			got, err := n.Normalize(raw)
			require.NoError(t, err)
			assert.Equal(t, sev, got.Severity)
		}
	})

	t.Run("missing title is rejected", func(t *testing.T) {
		raw := []byte(`{"severity": "high"}`)
		_, err := n.Normalize(raw)
		assert.ErrorContains(t, err, "title")
	})

	t.Run("empty title is rejected", func(t *testing.T) {
		raw := []byte(`{"title": "", "severity": "high"}`)
		_, err := n.Normalize(raw)
		assert.ErrorContains(t, err, "title")
	})

	t.Run("unknown severity is rejected", func(t *testing.T) {
		raw := []byte(`{"title": "t", "severity": "apocalyptic"}`)
		_, err := n.Normalize(raw)
		assert.ErrorContains(t, err, "severity")
	})

	t.Run("missing severity is rejected", func(t *testing.T) {
		raw := []byte(`{"title": "t"}`)
		_, err := n.Normalize(raw)
		assert.ErrorContains(t, err, "severity")
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(`{not json`))
		assert.Error(t, err)
	})

	t.Run("empty body is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(``))
		assert.Error(t, err)
	})
}
