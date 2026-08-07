package ingest_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/ingest"
)

func TestWazuhNormalizer_Normalize(t *testing.T) {
	n := ingest.NewWazuhNormalizer()

	t.Run("full valid envelope", func(t *testing.T) {
		raw := []byte(`{
			"id": "1700000000.123456",
			"rule": {"level": 12, "description": "Multiple authentication failures", "id": "5710", "groups": ["authentication_failed", "authentication_failures"]},
			"agent": {"name": "web-server-01", "ip": "10.0.0.10"},
			"data": {"srcip": "203.0.113.5"}
		}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		assert.Equal(t, "Multiple authentication failures", got.Title)
		assert.Equal(t, domain.SeverityCritical, got.Severity)
		require.NotNil(t, got.ExternalID)
		assert.Equal(t, "1700000000.123456", *got.ExternalID)
		require.NotNil(t, got.RuleID)
		assert.Equal(t, "5710", *got.RuleID)
		require.NotNil(t, got.Asset)
		assert.Equal(t, "web-server-01", *got.Asset)
		require.NotNil(t, got.SrcIP)
		assert.Equal(t, "203.0.113.5", *got.SrcIP, "data.srcip takes priority over agent.ip")
		assert.Equal(t, []string{"authentication_failed", "authentication_failures"}, got.Tags)
	})

	t.Run("falls back to agent.ip when data.srcip is absent", func(t *testing.T) {
		raw := []byte(`{"rule": {"level": 5, "description": "t"}, "agent": {"name": "a", "ip": "10.0.0.1"}}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		require.NotNil(t, got.SrcIP)
		assert.Equal(t, "10.0.0.1", *got.SrcIP)
	})

	t.Run("severity levels map into the expected bands", func(t *testing.T) {
		cases := []struct {
			level int
			want  domain.Severity
		}{
			{15, domain.SeverityCritical}, {12, domain.SeverityCritical},
			{11, domain.SeverityHigh}, {9, domain.SeverityHigh},
			{8, domain.SeverityMedium}, {6, domain.SeverityMedium},
			{5, domain.SeverityLow}, {3, domain.SeverityLow},
			{2, domain.SeverityInformational}, {0, domain.SeverityInformational},
		}
		for _, c := range cases {
			raw := []byte(`{"rule": {"level": ` + strconv.Itoa(c.level) + `, "description": "t"}}`)
			got, err := n.Normalize(raw)
			require.NoError(t, err)
			assert.Equal(t, c.want, got.Severity, "level %d", c.level)
		}
	})

	t.Run("missing rule.description is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(`{"rule": {"level": 5}}`))
		assert.ErrorContains(t, err, "rule.description")
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(`{not json`))
		assert.Error(t, err)
	})
}
