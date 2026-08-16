package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestTagService_CreateListDelete(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewTagService(pool, repository.NewTagRepository())

	t.Run("empty name is rejected", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, "   ", nil)
		assert.ErrorContains(t, err, "name is required")
	})

	t.Run("name is trimmed", func(t *testing.T) {
		tag, err := svc.Create(t.Context(), tenantID, actorID, "  phishing  ", nil)
		require.NoError(t, err)
		assert.Equal(t, "phishing", tag.Name)
	})

	// Regression: a duplicate name used to surface Postgres's raw
	// constraint-violation error (SQLSTATE 23505, tags_tenant_name_uq)
	// straight to the caller. Must now be a clean, actionable message.
	t.Run("duplicate name is rejected with a clean message, not a raw SQL error", func(t *testing.T) {
		_, err := svc.Create(t.Context(), tenantID, actorID, "phishing", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"phishing"`)
		assert.Contains(t, err.Error(), "already exists")
		assert.NotContains(t, err.Error(), "SQLSTATE")
		assert.NotContains(t, err.Error(), "constraint")
	})

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, svc.Delete(t.Context(), tenantID, list[0].ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestTagService_FilterKnown(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewTagService(pool, repository.NewTagRepository())

	_, err := svc.Create(t.Context(), tenantID, actorID, "vpn", nil)
	require.NoError(t, err)

	known, err := svc.FilterKnown(t.Context(), tenantID, []string{"vpn", "made-up"})
	require.NoError(t, err)
	assert.Equal(t, []string{"vpn"}, known)

	t.Run("cross-tenant tags are never visible", func(t *testing.T) {
		otherTenant := testutil.NewTenant(t)
		known, err := svc.FilterKnown(t.Context(), otherTenant, []string{"vpn"})
		require.NoError(t, err)
		assert.Empty(t, known)
	})
}
