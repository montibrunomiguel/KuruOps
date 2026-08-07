package ingest

import (
	"testing"
	"time"

	"github.com/argusops/argusops/internal/domain"
)

func TestCorrelationEngine_Deduplication(t *testing.T) {
	ce := NewCorrelationEngine(100 * time.Millisecond)

	ruleID := "SSH-001"
	alert1 := domain.Alert{
		Title:    "Brute force SSH attempt",
		Source:   "wazuh",
		RuleID:   &ruleID,
		Severity: domain.SeverityHigh,
		Tags:     []string{"empresa-a"},
	}

	fp1 := ce.ComputeFingerprint(alert1)
	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint")
	}

	if ce.IsDuplicate(fp1) {
		t.Fatal("first encounter should not be marked duplicate")
	}

	if !ce.IsDuplicate(fp1) {
		t.Fatal("second encounter within window should be marked duplicate")
	}

	time.Sleep(150 * time.Millisecond)
	if ce.IsDuplicate(fp1) {
		t.Fatal("encounter after window expiration should not be duplicate")
	}
}
