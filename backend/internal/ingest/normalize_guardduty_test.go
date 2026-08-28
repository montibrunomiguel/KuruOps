package ingest_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/ingest"
)

func TestGuardDutyNormalizer_Normalize(t *testing.T) {
	n := ingest.NewGuardDutyNormalizer()

	t.Run("full valid finding", func(t *testing.T) {
		raw := []byte(`{
			"id": "abcd1234ef",
			"type": "UnauthorizedAccess:EC2/SSHBruteForce",
			"title": "SSH brute force attack against i-0abcd1234",
			"severity": 5.0,
			"resource": {"instanceDetails": {"instanceId": "i-0abcd1234", "networkInterfaces": [{"publicIp": "198.51.100.5"}]}},
			"service": {"action": {"networkConnectionAction": {"remoteIpDetails": {"ipAddressV4": "203.0.113.9"}}}}
		}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		assert.Equal(t, "SSH brute force attack against i-0abcd1234", got.Title)
		assert.Equal(t, domain.SeverityMedium, got.Severity)
		require.NotNil(t, got.ExternalID)
		assert.Equal(t, "abcd1234ef", *got.ExternalID)
		require.NotNil(t, got.RuleID)
		assert.Equal(t, "UnauthorizedAccess:EC2/SSHBruteForce", *got.RuleID)
		require.NotNil(t, got.Asset)
		assert.Equal(t, "i-0abcd1234", *got.Asset)
		require.NotNil(t, got.SrcIP)
		assert.Equal(t, "203.0.113.9", *got.SrcIP, "the remote attacker IP takes priority over the instance's own public IP")
	})

	t.Run("falls back to instance public IP when no network connection action", func(t *testing.T) {
		raw := []byte(`{"title": "t", "severity": 2.0, "resource": {"instanceDetails": {"networkInterfaces": [{"publicIp": "198.51.100.5"}]}}}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		require.NotNil(t, got.SrcIP)
		assert.Equal(t, "198.51.100.5", *got.SrcIP)
	})

	t.Run("falls back to type when title is absent", func(t *testing.T) {
		raw := []byte(`{"type": "Recon:EC2/PortProbeUnprotectedPort", "severity": 2.0}`)
		got, err := n.Normalize(raw)
		require.NoError(t, err)
		assert.Equal(t, "Recon:EC2/PortProbeUnprotectedPort", got.Title)
	})

	t.Run("severity bands follow AWS's documented 3-tier scale", func(t *testing.T) {
		cases := []struct {
			score float64
			want  domain.Severity
		}{
			{8.9, domain.SeverityHigh}, {7.0, domain.SeverityHigh},
			{6.9, domain.SeverityMedium}, {4.0, domain.SeverityMedium},
			{3.9, domain.SeverityLow}, {0.1, domain.SeverityLow},
		}
		for _, c := range cases {
			raw := []byte(`{"title": "t", "severity": ` + strconv.FormatFloat(c.score, 'f', 1, 64) + `}`)
			got, err := n.Normalize(raw)
			require.NoError(t, err)
			assert.Equal(t, c.want, got.Severity, "score %v", c.score)
		}
	})

	t.Run("missing title and type is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(`{"severity": 5.0}`))
		assert.ErrorContains(t, err, "title")
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		_, err := n.Normalize([]byte(`{not json`))
		assert.Error(t, err)
	})
}
