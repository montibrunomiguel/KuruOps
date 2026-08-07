package repository_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

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
		Steps: map[domain.IncidentPhase][]string{
			domain.PhaseDetectionAnalysis: {"Verify sender", "Check headers"},
			domain.PhaseContainment:       {"Disable account"},
		},
	}
	require.NoError(t, repo.Insert(t.Context(), tx, pb))
	require.NotEqual(t, [16]byte{}, pb.ID)

	t.Run("get returns steps grouped by phase, in order", func(t *testing.T) {
		got, err := repo.Get(t.Context(), tx, pb.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Phishing Response", got.Title)
		assert.Equal(t, []string{"Verify sender", "Check headers"}, got.Steps[domain.PhaseDetectionAnalysis])
		assert.Equal(t, []string{"Disable account"}, got.Steps[domain.PhaseContainment])
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
		assert.Equal(t, []string{"Verify sender", "Check headers"}, list[0].Steps[domain.PhaseDetectionAnalysis])
	})

	t.Run("update replaces steps wholesale", func(t *testing.T) {
		pb.Title = "Phishing Response v2"
		pb.Steps = map[domain.IncidentPhase][]string{
			domain.PhaseRecovery: {"Reset password"},
		}
		require.NoError(t, repo.Update(t.Context(), tx, pb))

		got, err := repo.Get(t.Context(), tx, pb.ID)
		require.NoError(t, err)
		assert.Equal(t, "Phishing Response v2", got.Title)
		assert.Empty(t, got.Steps[domain.PhaseDetectionAnalysis])
		assert.Equal(t, []string{"Reset password"}, got.Steps[domain.PhaseRecovery])
	})

	t.Run("delete removes the playbook and its steps", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, pb.ID))
		got, err := repo.Get(t.Context(), tx, pb.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}
