package ingest

import (
	"encoding/json"
	"fmt"

	"github.com/kuruops/kuruops/internal/domain"
)

// wazuhNormalizer maps a Wazuh alert (as delivered by Wazuh's own webhook
// integration, e.g. a custom integration script or the wazuh-indexer
// output) to a domain.Alert. Built from Wazuh's publicly documented alert
// JSON shape, not exercised against real Wazuh traffic -- treat the
// severity-level mapping in particular as a starting point to validate
// against a real deployment, not a definitive scale (Wazuh's own docs
// describe rule.level's 16 levels only qualitatively, e.g. "12-15: severe
// attack").
type wazuhNormalizer struct{}

func NewWazuhNormalizer() Normalizer { return wazuhNormalizer{} }

type wazuhEnvelope struct {
	ID   string `json:"id"`
	Rule struct {
		Level       int      `json:"level"`
		Description string   `json:"description"`
		ID          string   `json:"id"`
		Groups      []string `json:"groups"`
	} `json:"rule"`
	Agent struct {
		Name string `json:"name"`
		IP   string `json:"ip"`
	} `json:"agent"`
	Data struct {
		SrcIP string `json:"srcip"`
	} `json:"data"`
}

func (wazuhNormalizer) Normalize(raw []byte) (NormalizedAlert, error) {
	var env wazuhEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return NormalizedAlert{}, fmt.Errorf("decode wazuh webhook body: %w", err)
	}
	if env.Rule.Description == "" {
		return NormalizedAlert{}, fmt.Errorf("wazuh webhook body missing required field: rule.description")
	}

	asset := env.Agent.Name
	srcIP := env.Data.SrcIP
	if srcIP == "" {
		srcIP = env.Agent.IP
	}

	na := NormalizedAlert{
		Title:    env.Rule.Description,
		Severity: wazuhSeverity(env.Rule.Level),
		Tags:     env.Rule.Groups,
	}
	if env.ID != "" {
		na.ExternalID = &env.ID
	}
	if env.Rule.ID != "" {
		na.RuleID = &env.Rule.ID
	}
	if asset != "" {
		na.Asset = &asset
	}
	if srcIP != "" {
		na.SrcIP = &srcIP
	}
	return na, nil
}

// wazuhSeverity maps rule.level (0-15+) to KuruOps' 5-tier severity,
// following the qualitative bands Wazuh's own documentation describes for
// the default ruleset ("0-3: low importance", ..., "12-15: severe attack").
func wazuhSeverity(level int) domain.Severity {
	switch {
	case level >= 12:
		return domain.SeverityCritical
	case level >= 9:
		return domain.SeverityHigh
	case level >= 6:
		return domain.SeverityMedium
	case level >= 3:
		return domain.SeverityLow
	default:
		return domain.SeverityInformational
	}
}
