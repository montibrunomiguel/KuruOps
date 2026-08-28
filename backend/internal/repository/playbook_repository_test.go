package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/testutil"
)

// stepTexts extracts just the text of each step, in order -- keeps
// assertions terse where a test doesn't care about step ids/webhook config.
func stepTexts(steps []domain.PlaybookStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Text
	}
	return out
}

func TestPlaybookRepository_InsertGetListUpdateDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewPlaybookRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	pb := &domain.Playbook{
		TenantID:    tenantID,
		Title:       "Phishing Response",
		Category:    "Phishing",
		Description: "Standard phishing triage",
		Keywords:    []string{"phishing", "credential"},
		Steps: map[domain.IncidentPhase][]domain.PlaybookStep{
			domain.PhaseDetectionAnalysis: {{Text: "Verify sender"}, {Text: "Check headers"}},
			domain.PhaseContainment:       {{Text: "Disable account"}},
		},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, pb))
	require.NotEqual(t, [16]byte{}, pb.ID)

	t.Run("get returns steps grouped by phase, in order, each with a stable id", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, pb.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Phishing Response", got.Title)
		assert.Equal(t, []string{"Verify sender", "Check headers"}, stepTexts(got.Steps[domain.PhaseDetectionAnalysis]))
		assert.Equal(t, []string{"Disable account"}, stepTexts(got.Steps[domain.PhaseContainment]))
		for _, phaseSteps := range got.Steps {
			for _, s := range phaseSteps {
				assert.NotEqual(t, uuid.Nil, s.ID)
			}
		}
	})

	t.Run("get unknown id returns nil, not an error", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("list includes the playbook with its steps", func(t *testing.T) {
		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, []string{"Verify sender", "Check headers"}, stepTexts(list[0].Steps[domain.PhaseDetectionAnalysis]))
	})

	t.Run("update replaces steps wholesale, generating fresh step ids", func(t *testing.T) {
		oldStepID := pb.Steps[domain.PhaseContainment][0].ID
		pb.Title = "Phishing Response v2"
		pb.Steps = map[domain.IncidentPhase][]domain.PlaybookStep{
			domain.PhaseRecovery: {{Text: "Reset password"}},
		}
		require.NoError(t, repo.Update(t.Context(), tx, pb))

		got, err := repo.Get(t.Context(), tx, pb.ID)
		require.NoError(t, err)
		assert.Equal(t, "Phishing Response v2", got.Title)
		assert.Empty(t, got.Steps[domain.PhaseDetectionAnalysis])
		assert.Equal(t, []string{"Reset password"}, stepTexts(got.Steps[domain.PhaseRecovery]))
		assert.NotEqual(t, oldStepID, got.Steps[domain.PhaseRecovery][0].ID, "a fresh id is generated on every replaceSteps, not reused")
	})

	t.Run("delete removes the playbook and its steps", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, pb.ID))
		got, err := repo.Get(t.Context(), tx, pb.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestPlaybookRepository_StepWebhookConfig(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewPlaybookRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	pb := &domain.Playbook{
		TenantID: tenantID, Title: "Ransomware Response", Category: "Ransomware",
		Steps: map[domain.IncidentPhase][]domain.PlaybookStep{
			domain.PhaseContainment: {
				{Text: "Isolate host", WebhookURL: "https://hooks.example/isolate", WebhookPayloadTemplate: `{"host":"{{title}}"}`},
				{Text: "Notify legal"},
			},
		},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, pb))

	// Insert/replaceSteps never scan the DB-generated step ids back onto
	// pb.Steps (a delete-then-reinsert has nothing to scan into) -- a fresh
	// Get is required to learn the real ids.
	inserted, err := repo.Get(t.Context(), tx, pb.ID)
	require.NoError(t, err)
	steps := inserted.Steps[domain.PhaseContainment]

	t.Run("round-trips webhook_url/webhook_payload_template per step", func(t *testing.T) {
		require.Len(t, steps, 2)
		assert.Equal(t, "https://hooks.example/isolate", steps[0].WebhookURL)
		assert.Equal(t, `{"host":"{{title}}"}`, steps[0].WebhookPayloadTemplate)
		assert.Empty(t, steps[1].WebhookURL)
	})

	t.Run("GetStepWebhookConfig finds a step with webhook configured", func(t *testing.T) {
		playbookID, url, template, found, err := repo.GetStepWebhookConfig(t.Context(), tx, steps[0].ID)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, pb.ID, playbookID)
		assert.Equal(t, "https://hooks.example/isolate", url)
		assert.Equal(t, `{"host":"{{title}}"}`, template)
	})

	t.Run("GetStepWebhookConfig reports not-found for a step with no webhook configured", func(t *testing.T) {
		_, _, _, found, err := repo.GetStepWebhookConfig(t.Context(), tx, steps[1].ID)
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("GetStepWebhookConfig reports not-found for an unknown step id", func(t *testing.T) {
		_, _, _, found, err := repo.GetStepWebhookConfig(t.Context(), tx, uuid.New())
		require.NoError(t, err)
		assert.False(t, found)
	})
}

func TestPlaybookRepository_DefaultFlag(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewPlaybookRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	first := &domain.Playbook{TenantID: tenantID, Title: "General Response", Category: "General", IsDefault: true}
	require.NoError(t, repo.Insert(t.Context(), tx, first))

	t.Run("saving a second default unsets the first", func(t *testing.T) {
		second := &domain.Playbook{TenantID: tenantID, Title: "New Default", Category: "General", IsDefault: true}
		require.NoError(t, repo.Insert(t.Context(), tx, second))

		gotFirst, err := repo.Get(t.Context(), tx, first.ID)
		require.NoError(t, err)
		assert.False(t, gotFirst.IsDefault)

		gotSecond, err := repo.Get(t.Context(), tx, second.ID)
		require.NoError(t, err)
		assert.True(t, gotSecond.IsDefault)
	})

	t.Run("update can also flip the default to a different playbook", func(t *testing.T) {
		third := &domain.Playbook{TenantID: tenantID, Title: "Third Playbook", Category: "General"}
		require.NoError(t, repo.Insert(t.Context(), tx, third))

		third.IsDefault = true
		require.NoError(t, repo.Update(t.Context(), tx, third))

		list, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		defaults := 0
		for _, pb := range list {
			if pb.IsDefault {
				defaults++
				assert.Equal(t, third.ID, pb.ID)
			}
		}
		assert.Equal(t, 1, defaults, "at most one playbook stays marked default per tenant")
	})
}

func TestPlaybookRepository_MatchForAlertTitle(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewPlaybookRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("no playbooks configured -- nil, not an error", func(t *testing.T) {
		got, err := repo.MatchForAlertTitle(t.Context(), tx, "Suspicious login from unusual location")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	def := &domain.Playbook{TenantID: tenantID, Title: "General Security Event", Category: "General", IsDefault: true}
	require.NoError(t, repo.Insert(t.Context(), tx, def))

	t.Run("falls back to the default playbook when no pattern matches", func(t *testing.T) {
		got, err := repo.MatchForAlertTitle(t.Context(), tx, "Something entirely unrelated")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, def.ID, got.ID)
	})

	generic := &domain.Playbook{TenantID: tenantID, Title: "Login Response", Category: "Auth", AlertNamePattern: "Suspicious login%"}
	require.NoError(t, repo.Insert(t.Context(), tx, generic))

	t.Run("a matching pattern wins over the default", func(t *testing.T) {
		got, err := repo.MatchForAlertTitle(t.Context(), tx, "Suspicious login from Brazil")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, generic.ID, got.ID)
	})

	t.Run("matching is case-insensitive", func(t *testing.T) {
		got, err := repo.MatchForAlertTitle(t.Context(), tx, "SUSPICIOUS LOGIN from Brazil")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, generic.ID, got.ID)
	})

	specific := &domain.Playbook{TenantID: tenantID, Title: "Brazil Login Response", Category: "Auth", AlertNamePattern: "Suspicious login from Brazil%"}
	require.NoError(t, repo.Insert(t.Context(), tx, specific))

	t.Run("the more specific (longer) pattern wins when several match", func(t *testing.T) {
		got, err := repo.MatchForAlertTitle(t.Context(), tx, "Suspicious login from Brazil detected")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, specific.ID, got.ID)
	})

	t.Run("no pattern match and no default -- nil", func(t *testing.T) {
		noDefaultTenant := testutil.NewTenant(t)
		noDefaultTx := testutil.BeginTx(t, pool, noDefaultTenant)
		pb := &domain.Playbook{TenantID: noDefaultTenant, Title: "Unrelated", Category: "Other", AlertNamePattern: "Malware%"}
		require.NoError(t, repo.Insert(t.Context(), noDefaultTx, pb))

		got, err := repo.MatchForAlertTitle(t.Context(), noDefaultTx, "Suspicious login")
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
