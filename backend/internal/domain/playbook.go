package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Playbook mirrors `playbooks` + `playbook_phase_steps`. Steps are grouped
// by NIST phase, one ordered slice of action lines per phase — matches the
// "one textarea per NIST phase, one action per line" create/edit form in
// the design handoff.
type Playbook struct {
	ID          uuid.UUID                  `json:"id"`
	TenantID    uuid.UUID                  `json:"tenantId"`
	Title       string                     `json:"title"`
	Category    string                     `json:"category"`
	Description string                     `json:"description"`
	Keywords    []string                   `json:"keywords"`
	Steps       map[IncidentPhase][]string `json:"steps"`
	CreatedBy   *uuid.UUID                 `json:"createdBy,omitempty"`
	CreatedAt   time.Time                  `json:"createdAt"`
	UpdatedAt   time.Time                  `json:"updatedAt"`
}

type SavePlaybookInput struct {
	Title       string
	Category    string
	Description string
	Keywords    []string
	Steps       map[IncidentPhase][]string
}

// MatchesTitle reports whether any keyword substring-matches (case
// insensitive) the given alert title — the auto-suggestion rule from the
// design handoff ("Related Playbook" panel on Alert Detail).
func (p Playbook) MatchesTitle(alertTitle string) bool {
	lowered := strings.ToLower(alertTitle)
	for _, kw := range p.Keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(lowered, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
