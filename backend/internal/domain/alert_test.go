package domain_test

import (
	"testing"

	"github.com/argusops/argusops/internal/domain"
	"github.com/google/uuid"
)

func TestAlertSeverityConstants(t *testing.T) {
	tests := []struct {
		severity domain.Severity
		expected string
	}{
		{domain.SeverityCritical, "critical"},
		{domain.SeverityHigh, "high"},
		{domain.SeverityMedium, "medium"},
		{domain.SeverityLow, "low"},
		{domain.SeverityInformational, "informational"},
	}

	for _, tt := range tests {
		if string(tt.severity) != tt.expected {
			t.Errorf("expected severity %s, got %s", tt.expected, tt.severity)
		}
	}
}

func TestAlertStatusTransitions(t *testing.T) {
	alert := domain.Alert{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		Title:    "Suspicious Login",
		Status:   domain.AlertStatusOpen,
		Severity: domain.SeverityHigh,
	}

	if alert.Status != domain.AlertStatusOpen {
		t.Fatalf("expected initial status to be open, got %s", alert.Status)
	}

	// Change to investigating
	alert.Status = domain.AlertStatusInvestigating
	if alert.Status != domain.AlertStatusInvestigating {
		t.Fatalf("expected status to be investigating, got %s", alert.Status)
	}

	// Close with classification
	classification := domain.ClassificationTruePositive
	alert.Status = domain.AlertStatusClosed
	alert.Classification = &classification

	if alert.Status != domain.AlertStatusClosed {
		t.Fatalf("expected status to be closed, got %s", alert.Status)
	}

	if alert.Classification == nil || *alert.Classification != domain.ClassificationTruePositive {
		t.Fatalf("expected classification to be true_positive, got %v", alert.Classification)
	}
}
