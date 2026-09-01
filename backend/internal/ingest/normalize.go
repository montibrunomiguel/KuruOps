// Package ingest turns a source-specific webhook payload into a
// domain.Alert. Each SIEM/XDR speaks its own JSON dialect (Wazuh nests
// severity under rule.level, CrowdStrike calls it something else entirely),
// so normalization is a small per-source adapter behind one interface
// rather than one handler trying to branch on every vendor's shape.
package ingest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kuruops/kuruops/internal/domain"
)

// Normalizer extracts the fields KuruOps needs to open an alert from a raw
// webhook body. The full body is always kept as domain.Alert.Payload
// regardless of what the normalizer manages to extract, so nothing is lost
// even when a field is missing or a new source isn't fully mapped yet.
type Normalizer interface {
	Normalize(raw []byte) (NormalizedAlert, error)
}

type NormalizedAlert struct {
	Title      string
	Severity   domain.Severity
	ExternalID *string
	RuleID     *string
	Asset      *string
	SrcIP      *string
	// Tags are whatever the source payload sent -- the ingest handler
	// auto-creates any of these that aren't already in the tenant's tag
	// catalog before the alert is inserted (see TagService.EnsureExist), so
	// nothing here is ever dropped or rejected as an ingest error.
	Tags []string
}

// genericNormalizer is the fallback used for sources without a dedicated
// adapter. It expects a flat envelope: {"title", "severity", ...}. Add a
// dedicated Normalizer per source (Wazuh, CrowdStrike, Azure AD Identity
// Protection, ...) as each is onboarded — see README for the pattern.
type genericNormalizer struct{}

func NewGenericNormalizer() Normalizer {
	return genericNormalizer{}
}

type genericEnvelope struct {
	Title      string   `json:"title"`
	Severity   string   `json:"severity"`
	ExternalID *string  `json:"external_id"`
	RuleID     *string  `json:"rule_id"`
	Asset      *string  `json:"asset"`
	SrcIP      *string  `json:"src_ip"`
	Tags       []string `json:"tags"`
}

func (genericNormalizer) Normalize(raw []byte) (NormalizedAlert, error) {
	var env genericEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return NormalizedAlert{}, fmt.Errorf("decode webhook body: %w", err)
	}
	if env.Title == "" {
		return NormalizedAlert{}, fmt.Errorf("webhook body missing required field: title")
	}

	severity, ok := parseSeverity(env.Severity)
	if !ok {
		return NormalizedAlert{}, fmt.Errorf(
			"webhook body has invalid severity: %q (accepted: critical, high, medium, low, informational)",
			env.Severity)
	}

	return NormalizedAlert{
		Title:      env.Title,
		Severity:   severity,
		ExternalID: env.ExternalID,
		RuleID:     env.RuleID,
		Asset:      env.Asset,
		SrcIP:      env.SrcIP,
		Tags:       env.Tags,
	}, nil
}

// severityAliases maps the spellings other products actually emit onto this
// one's vocabulary. Matching was previously exact and case-sensitive, so a
// source sending "High" -- which most SIEM and EDR products do -- had every
// alert rejected with a 400 at the door. The dedicated Wazuh/CrowdStrike/
// GuardDuty normalizers translate their own vendor scales; this is the
// generic path, which is what every customer-built integration hits.
var severityAliases = map[string]domain.Severity{
	"critical": domain.SeverityCritical,
	"crit":     domain.SeverityCritical,
	"fatal":    domain.SeverityCritical,
	"sev1":     domain.SeverityCritical,
	"high":     domain.SeverityHigh,
	"error":    domain.SeverityHigh,
	"err":      domain.SeverityHigh,
	"sev2":     domain.SeverityHigh,
	"medium":   domain.SeverityMedium,
	"moderate": domain.SeverityMedium,
	"warning":  domain.SeverityMedium,
	"warn":     domain.SeverityMedium,
	"sev3":     domain.SeverityMedium,
	"low":      domain.SeverityLow,
	"minor":    domain.SeverityLow,
	"sev4":     domain.SeverityLow,

	"informational": domain.SeverityInformational,
	"info":          domain.SeverityInformational,
	"information":   domain.SeverityInformational,
	"notice":        domain.SeverityInformational,
	"debug":         domain.SeverityInformational,
}

// parseSeverity resolves a source's severity string, tolerating case and
// surrounding whitespace. An unrecognised value is still rejected rather
// than silently bucketed -- guessing a severity would be worse than telling
// the sender its value is not understood.
func parseSeverity(raw string) (domain.Severity, bool) {
	sev, ok := severityAliases[strings.ToLower(strings.TrimSpace(raw))]
	return sev, ok
}
