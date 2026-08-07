package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/domain"
)

func TestPlaybook_MatchesTitle(t *testing.T) {
	pb := domain.Playbook{Keywords: []string{"phishing", "Credential Theft"}}

	cases := []struct {
		name  string
		title string
		want  bool
	}{
		{"case-insensitive substring match", "Suspicious PHISHING email reported", true},
		{"matches a multi-word keyword case-insensitively", "possible credential theft detected", true},
		{"no keyword present", "Malware detected on endpoint", false},
		{"empty title", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, pb.MatchesTitle(c.title))
		})
	}

	t.Run("empty keywords in the list are ignored, not treated as match-everything", func(t *testing.T) {
		withEmpty := domain.Playbook{Keywords: []string{"", "ransomware"}}
		assert.False(t, withEmpty.MatchesTitle("Suspicious login from unusual location"))
		assert.True(t, withEmpty.MatchesTitle("Ransomware note found on shared drive"))
	})

	t.Run("no keywords never matches", func(t *testing.T) {
		empty := domain.Playbook{}
		assert.False(t, empty.MatchesTitle("anything at all"))
	})
}
