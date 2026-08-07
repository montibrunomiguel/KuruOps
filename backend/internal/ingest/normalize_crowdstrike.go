package ingest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/argusops/argusops/internal/domain"
)

// crowdstrikeNormalizer maps a CrowdStrike Falcon detection event (the
// Falcon Streaming API's DetectionSummaryEvent shape, as a customer would
// forward it via their own webhook relay -- CrowdStrike doesn't push
// webhooks directly) to a domain.Alert. Built from CrowdStrike's publicly
// documented event schema, not exercised against real Falcon traffic.
type crowdstrikeNormalizer struct{}

func NewCrowdStrikeNormalizer() Normalizer { return crowdstrikeNormalizer{} }

type crowdstrikeEnvelope struct {
	Event struct {
		DetectId     string `json:"DetectId"`
		DetectName   string `json:"DetectName"`
		Severity     int    `json:"Severity"`
		SeverityName string `json:"SeverityName"`
		ComputerName string `json:"ComputerName"`
		LocalIP      string `json:"LocalIP"`
		Tactic       string `json:"Tactic"`
		Technique    string `json:"Technique"`
	} `json:"event"`
}

func (crowdstrikeNormalizer) Normalize(raw []byte) (NormalizedAlert, error) {
	var env crowdstrikeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return NormalizedAlert{}, fmt.Errorf("decode crowdstrike webhook body: %w", err)
	}
	if env.Event.DetectName == "" {
		return NormalizedAlert{}, fmt.Errorf("crowdstrike webhook body missing required field: event.DetectName")
	}

	na := NormalizedAlert{
		Title:    env.Event.DetectName,
		Severity: crowdstrikeSeverity(env.Event.SeverityName, env.Event.Severity),
	}
	if env.Event.DetectId != "" {
		na.ExternalID = &env.Event.DetectId
	}
	if env.Event.ComputerName != "" {
		na.Asset = &env.Event.ComputerName
	}
	if env.Event.LocalIP != "" {
		na.SrcIP = &env.Event.LocalIP
	}
	for _, tag := range []string{env.Event.Tactic, env.Event.Technique} {
		if tag != "" {
			na.Tags = append(na.Tags, tag)
		}
	}
	return na, nil
}

// crowdstrikeSeverity prefers SeverityName (CrowdStrike's own human label,
// e.g. "Critical"/"High"/"Medium"/"Low"/"Informational") when present,
// falling back to the 0-100 numeric Severity score's approximate bands
// (per CrowdStrike's documented "Critical: 90-100, High: 70-89, Medium:
// 40-69, Low: 20-39" convention) since not every event includes the name.
func crowdstrikeSeverity(name string, score int) domain.Severity {
	switch strings.ToLower(name) {
	case "critical":
		return domain.SeverityCritical
	case "high":
		return domain.SeverityHigh
	case "medium":
		return domain.SeverityMedium
	case "low":
		return domain.SeverityLow
	case "informational", "info":
		return domain.SeverityInformational
	}

	switch {
	case score >= 90:
		return domain.SeverityCritical
	case score >= 70:
		return domain.SeverityHigh
	case score >= 40:
		return domain.SeverityMedium
	case score >= 20:
		return domain.SeverityLow
	default:
		return domain.SeverityInformational
	}
}
