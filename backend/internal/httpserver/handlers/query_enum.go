package handlers

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/kuruops/kuruops/internal/domain"
)

var (
	validSeverities = []domain.Severity{
		domain.SeverityCritical, domain.SeverityHigh, domain.SeverityMedium, domain.SeverityLow, domain.SeverityInformational,
	}
	validAlertStatuses = []domain.AlertStatus{
		domain.AlertStatusOpen, domain.AlertStatusInvestigating, domain.AlertStatusEscalated, domain.AlertStatusClosed,
	}
	validIncidentPhases = []domain.IncidentPhase{
		domain.PhaseNew, domain.PhaseDetectionAnalysis, domain.PhaseContainment,
		domain.PhaseEradication, domain.PhaseRecovery, domain.PhasePostIncident,
	}
	validIncidentPriorities = []domain.IncidentPriority{
		domain.PriorityP1, domain.PriorityP2, domain.PriorityP3, domain.PriorityP4,
	}
)

// parseEnumListQueryParam is parseStringListQueryParam plus a check that every
// value is one of allowed. A value outside the enum is the caller's mistake and
// is answered with an error (a 400 at the call site) -- handing it to Postgres
// instead makes the enum cast fail, which surfaces as a 500 plus an error log
// for what is only a bad query string.
func parseEnumListQueryParam[T ~string](r *http.Request, name string, allowed []T) ([]T, error) {
	values := parseStringListQueryParam[T](r, name)
	for _, v := range values {
		if err := checkEnumValue(name, v, allowed); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func checkEnumValue[T ~string](name string, v T, allowed []T) error {
	if slices.Contains(allowed, v) {
		return nil
	}
	shown := string(v)
	if len(shown) > 64 {
		shown = shown[:64] + "..."
	}
	return fmt.Errorf("invalid %s %q", name, shown)
}
