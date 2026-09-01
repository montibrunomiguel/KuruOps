package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

// TestPlaybookService_RejectsNewPhaseSteps covers the phase the product
// stopped offering. The API kept accepting it, so a step written through the
// API was stored, counted, and rendered nowhere -- invisible and uneditable
// in the UI it belongs to.
func TestPlaybookService_RejectsNewPhaseSteps(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewPlaybookService(pool, repository.NewPlaybookRepository(), repository.NewAlertRepository(), "https://kuruops.example")

	input := func(steps map[domain.IncidentPhase][]domain.SavePlaybookStepInput) domain.SavePlaybookInput {
		return domain.SavePlaybookInput{Title: "PB", Category: "Test", Steps: steps}
	}

	t.Run("Create refuses a step in the new phase", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, input(map[domain.IncidentPhase][]domain.SavePlaybookStepInput{
			domain.PhaseNew: {{Text: "should not be allowed"}},
		}))
		require.Error(t, err)
		assert.ErrorContains(t, err, "new")
	})

	t.Run("Update refuses it too", func(t *testing.T) {
		pb, err := svc.Create(t.Context(), tenantID, actorID, input(map[domain.IncidentPhase][]domain.SavePlaybookStepInput{
			domain.PhaseContainment: {{Text: "isolate the host"}},
		}))
		require.NoError(t, err)

		_, err = svc.Update(t.Context(), tenantID, pb.ID, input(map[domain.IncidentPhase][]domain.SavePlaybookStepInput{
			domain.PhaseNew: {{Text: "sneaking it in on edit"}},
		}))
		assert.Error(t, err)
	})

	t.Run("an empty new-phase entry is not treated as a step", func(t *testing.T) {
		// A client sending "new": [] is saying nothing, not asking for a
		// forbidden step -- rejecting that would break honest callers.
		_, err := svc.Create(t.Context(), tenantID, actorID, input(map[domain.IncidentPhase][]domain.SavePlaybookStepInput{
			domain.PhaseNew:               {},
			domain.PhaseDetectionAnalysis: {{Text: "confirm the alert"}},
		}))
		assert.NoError(t, err)
	})
}

// TestPlaybookService_KeywordMatching pins the behaviour that keywords are
// actually consulted. They were collected in the editor, stored, and then
// never used by the matcher at all -- a playbook could list every keyword an
// analyst could think of and still match nothing.
func TestPlaybookService_KeywordMatching(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewPlaybookService(pool, repository.NewPlaybookRepository(), repository.NewAlertRepository(), "https://kuruops.example")

	_, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "Credential Access", Category: "TA0006",
		Keywords: []string{"lsass", "mimikatz", "ntds.dit"},
		Steps:    map[domain.IncidentPhase][]domain.SavePlaybookStepInput{domain.PhaseContainment: {{Text: "isolate"}}},
	})
	require.NoError(t, err)

	t.Run("a keyword appearing anywhere in the title matches", func(t *testing.T) {
		pb, err := svc.MatchForAlertTitle(t.Context(), tenantID, "Dump de LSASS detectado")
		require.NoError(t, err)
		require.NotNil(t, pb, "the keyword 'lsass' occurs in the title")
		assert.Equal(t, "Credential Access", pb.Title)
	})

	t.Run("keyword matching ignores case", func(t *testing.T) {
		pb, err := svc.MatchForAlertTitle(t.Context(), tenantID, "MIMIKATZ observed on host")
		require.NoError(t, err)
		require.NotNil(t, pb)
	})

	t.Run("a title sharing no keyword still matches nothing", func(t *testing.T) {
		pb, err := svc.MatchForAlertTitle(t.Context(), tenantID, "Impressora sem papel")
		require.NoError(t, err)
		assert.Nil(t, pb)
	})

	t.Run("an explicit pattern outranks a keyword hit", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
			Title: "Exact pattern", Category: "TA0006", AlertNamePattern: "%LSASS%",
			Steps: map[domain.IncidentPhase][]domain.SavePlaybookStepInput{domain.PhaseContainment: {{Text: "x"}}},
		})
		require.NoError(t, err)

		pb, err := svc.MatchForAlertTitle(t.Context(), tenantID, "Dump de LSASS detectado")
		require.NoError(t, err)
		require.NotNil(t, pb)
		assert.Equal(t, "Exact pattern", pb.Title,
			"a pattern is the more specific statement of intent, so it wins over a keyword")
	})
}
