package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/argusops/argusops/internal/domain"
)

func TestIncidentPhase_Index(t *testing.T) {
	cases := []struct {
		phase domain.IncidentPhase
		want  int
	}{
		{domain.PhaseNew, 0},
		{domain.PhaseDetectionAnalysis, 1},
		{domain.PhaseContainment, 2},
		{domain.PhaseEradication, 3},
		{domain.PhaseRecovery, 4},
		{domain.PhasePostIncident, 5},
		{domain.IncidentPhase("bogus"), -1},
		{domain.IncidentPhase(""), -1},
	}
	for _, c := range cases {
		t.Run(string(c.phase), func(t *testing.T) {
			assert.Equal(t, c.want, c.phase.Index())
		})
	}
}

func TestNISTPhaseOrder_MatchesConstants(t *testing.T) {
	// Every declared phase constant must appear exactly once in the
	// canonical order, and nothing else -- isForwardSkip in
	// IncidentService depends on this list being complete and ordered.
	assert.Equal(t, []domain.IncidentPhase{
		domain.PhaseNew,
		domain.PhaseDetectionAnalysis,
		domain.PhaseContainment,
		domain.PhaseEradication,
		domain.PhaseRecovery,
		domain.PhasePostIncident,
	}, domain.NISTPhaseOrder)
}

func TestIncidentStatusHistoryEntry_EffectiveEnteredAt(t *testing.T) {
	original := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	corrected := time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)

	t.Run("no correction returns the original timestamp", func(t *testing.T) {
		e := domain.IncidentStatusHistoryEntry{EnteredAt: original}
		assert.Equal(t, original, e.EffectiveEnteredAt())
	})

	t.Run("a correction takes precedence over the original", func(t *testing.T) {
		e := domain.IncidentStatusHistoryEntry{EnteredAt: original, CorrectedEnteredAt: &corrected}
		assert.Equal(t, corrected, e.EffectiveEnteredAt())
	})
}
