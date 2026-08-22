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

func TestRetentionConfigService_Get(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewRetentionConfigService(pool, repository.NewRetentionConfigRepository())

	t.Run("unconfigured tenant gets the synthesized default, not nil", func(t *testing.T) {
		cfg, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, domain.DefaultRetentionMonths, cfg.AlertRetentionMonths)
		assert.Equal(t, domain.DefaultRetentionMonths, cfg.IncidentRetentionMonths)
		assert.False(t, cfg.Configured)
	})

	require.NoError(t, svc.Save(t.Context(), tenantID, service.SaveRetentionInput{
		AlertRetentionMonths: 6, IncidentRetentionMonths: 36,
	}))

	t.Run("after an explicit save, Get returns the real values and Configured=true", func(t *testing.T) {
		cfg, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, 6, cfg.AlertRetentionMonths)
		assert.Equal(t, 36, cfg.IncidentRetentionMonths)
		assert.True(t, cfg.Configured)
	})
}

func TestRetentionConfigService_Save(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewRetentionConfigService(pool, repository.NewRetentionConfigRepository())

	t.Run("zero months is allowed", func(t *testing.T) {
		require.NoError(t, svc.Save(t.Context(), tenantID, service.SaveRetentionInput{
			AlertRetentionMonths: 0, IncidentRetentionMonths: 0,
		}))
		cfg, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, 0, cfg.AlertRetentionMonths)
		assert.Equal(t, 0, cfg.IncidentRetentionMonths)
	})

	t.Run("negative alert months is rejected", func(t *testing.T) {
		err := svc.Save(t.Context(), tenantID, service.SaveRetentionInput{AlertRetentionMonths: -1, IncidentRetentionMonths: 18})
		assert.ErrorContains(t, err, "zero or positive")
	})

	t.Run("negative incident months is rejected", func(t *testing.T) {
		err := svc.Save(t.Context(), tenantID, service.SaveRetentionInput{AlertRetentionMonths: 18, IncidentRetentionMonths: -1})
		assert.ErrorContains(t, err, "zero or positive")
	})
}
