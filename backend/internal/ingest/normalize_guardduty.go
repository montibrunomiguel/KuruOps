package ingest

import (
	"encoding/json"
	"fmt"

	"github.com/argusops/argusops/internal/domain"
)

// guardDutyNormalizer maps an AWS GuardDuty finding (the JSON shape AWS
// documents for a finding, as delivered via an EventBridge rule forwarding
// to a webhook relay -- GuardDuty has no native webhook output) to a
// domain.Alert. Built from AWS's publicly documented finding schema, not
// exercised against real GuardDuty traffic.
type guardDutyNormalizer struct{}

func NewGuardDutyNormalizer() Normalizer { return guardDutyNormalizer{} }

type guardDutyEnvelope struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Title    string  `json:"title"`
	Severity float64 `json:"severity"`
	Resource struct {
		InstanceDetails struct {
			InstanceID        string `json:"instanceId"`
			NetworkInterfaces []struct {
				PublicIP string `json:"publicIp"`
			} `json:"networkInterfaces"`
		} `json:"instanceDetails"`
	} `json:"resource"`
	Service struct {
		Action struct {
			NetworkConnectionAction struct {
				RemoteIPDetails struct {
					IPAddressV4 string `json:"ipAddressV4"`
				} `json:"remoteIpDetails"`
			} `json:"networkConnectionAction"`
		} `json:"action"`
	} `json:"service"`
}

func (guardDutyNormalizer) Normalize(raw []byte) (NormalizedAlert, error) {
	var env guardDutyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return NormalizedAlert{}, fmt.Errorf("decode guardduty webhook body: %w", err)
	}

	title := env.Title
	if title == "" {
		title = env.Type
	}
	if title == "" {
		return NormalizedAlert{}, fmt.Errorf("guardduty webhook body missing required field: title (and type, as fallback)")
	}

	srcIP := env.Service.Action.NetworkConnectionAction.RemoteIPDetails.IPAddressV4
	if srcIP == "" && len(env.Resource.InstanceDetails.NetworkInterfaces) > 0 {
		srcIP = env.Resource.InstanceDetails.NetworkInterfaces[0].PublicIP
	}

	na := NormalizedAlert{
		Title:    title,
		Severity: guardDutySeverity(env.Severity),
	}
	if env.ID != "" {
		na.ExternalID = &env.ID
	}
	if env.Type != "" {
		na.RuleID = &env.Type
	}
	if env.Resource.InstanceDetails.InstanceID != "" {
		na.Asset = &env.Resource.InstanceDetails.InstanceID
	}
	if srcIP != "" {
		na.SrcIP = &srcIP
	}
	return na, nil
}

// guardDutySeverity maps GuardDuty's 0.1-8.9 float score to ArgusOps'
// 5-tier severity, using AWS's own documented 3-tier bands (High 7.0-8.9,
// Medium 4.0-6.9, Low 0.1-3.9) -- GuardDuty has no native "critical" tier,
// so that severity is simply never produced by this normalizer.
func guardDutySeverity(score float64) domain.Severity {
	switch {
	case score >= 7.0:
		return domain.SeverityHigh
	case score >= 4.0:
		return domain.SeverityMedium
	default:
		return domain.SeverityLow
	}
}
