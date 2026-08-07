package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestPlaybookService_CRUD(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewPlaybookService(pool, repository.NewPlaybookRepository())

	pb, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "Phishing Response", Category: "Phishing",
		Steps: map[domain.IncidentPhase][]string{domain.PhaseContainment: {"Disable account"}},
	})
	require.NoError(t, err)
	assert.NotNil(t, pb.CreatedBy)

	t.Run("get", func(t *testing.T) {
		got, err := svc.Get(t.Context(), tenantID, pb.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "Phishing Response", got.Title)
	})

	t.Run("nil keywords normalize to an empty slice, not NULL", func(t *testing.T) {
		got, err := svc.Get(t.Context(), tenantID, pb.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{}, got.Keywords)
	})

	t.Run("update", func(t *testing.T) {
		updated, err := svc.Update(t.Context(), tenantID, pb.ID, domain.SavePlaybookInput{
			Title: "Phishing Response v2", Category: "Phishing",
			Steps: map[domain.IncidentPhase][]string{domain.PhaseRecovery: {"Reset password"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "Phishing Response v2", updated.Title)
	})

	t.Run("delete", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID, pb.ID))
		got, err := svc.Get(t.Context(), tenantID, pb.ID)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestPlaybookService_MatchForAlertTitle(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewPlaybookService(pool, repository.NewPlaybookRepository())

	_, err := svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "Phishing Response", Category: "Phishing", Keywords: []string{"phishing", "credential"},
	})
	require.NoError(t, err)
	_, err = svc.Create(t.Context(), tenantID, actorID, domain.SavePlaybookInput{
		Title: "General Response", Category: "General Security Event",
	})
	require.NoError(t, err)

	t.Run("matches by keyword substring, case-insensitive", func(t *testing.T) {
		match, err := svc.MatchForAlertTitle(t.Context(), tenantID, "Possible PHISHING attempt detected")
		require.NoError(t, err)
		require.NotNil(t, match)
		assert.Equal(t, "Phishing Response", match.Title)
	})

	t.Run("falls back to the general category when nothing matches", func(t *testing.T) {
		match, err := svc.MatchForAlertTitle(t.Context(), tenantID, "Unrelated alert about disk space")
		require.NoError(t, err)
		require.NotNil(t, match)
		assert.Equal(t, "General Response", match.Title)
	})

	t.Run("returns nil when there's no match and no fallback category", func(t *testing.T) {
		otherTenant := testutil.NewTenant(t)
		match, err := svc.MatchForAlertTitle(t.Context(), otherTenant, "Anything")
		require.NoError(t, err)
		assert.Nil(t, match)
	})
}
