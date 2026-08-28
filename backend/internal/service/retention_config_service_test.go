package service_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestRetentionConfigService_Get(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewRetentionConfigService(pool, repository.NewRetentionConfigRepository(), repository.NewAdminAuditEventRepository())

	t.Run("unconfigured tenant gets the synthesized default, not nil", func(t *testing.T) {
		cfg, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, domain.DefaultRetentionMonths, cfg.AlertRetentionMonths)
		assert.Equal(t, domain.DefaultRetentionMonths, cfg.IncidentRetentionMonths)
		assert.False(t, cfg.Configured)
	})

	require.NoError(t, svc.Save(t.Context(), tenantID, actorID, service.SaveRetentionInput{
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
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewRetentionConfigService(pool, repository.NewRetentionConfigRepository(), auditRepo)

	t.Run("zero months is allowed", func(t *testing.T) {
		require.NoError(t, svc.Save(t.Context(), tenantID, actorID, service.SaveRetentionInput{
			AlertRetentionMonths: 0, IncidentRetentionMonths: 0,
		}))
		cfg, err := svc.Get(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Equal(t, 0, cfg.AlertRetentionMonths)
		assert.Equal(t, 0, cfg.IncidentRetentionMonths)
	})

	t.Run("negative alert months is rejected", func(t *testing.T) {
		err := svc.Save(t.Context(), tenantID, actorID, service.SaveRetentionInput{AlertRetentionMonths: -1, IncidentRetentionMonths: 18})
		assert.ErrorContains(t, err, "zero or positive")
	})

	t.Run("negative incident months is rejected", func(t *testing.T) {
		err := svc.Save(t.Context(), tenantID, actorID, service.SaveRetentionInput{AlertRetentionMonths: 18, IncidentRetentionMonths: -1})
		assert.ErrorContains(t, err, "zero or positive")
	})

	t.Run("a successful save records an admin audit event with the before/after values", func(t *testing.T) {
		require.NoError(t, svc.Save(t.Context(), tenantID, actorID, service.SaveRetentionInput{
			AlertRetentionMonths: 3, IncidentRetentionMonths: 9,
		}))
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 1)
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "retention", events[0].Area)
		assert.Equal(t, "save", events[0].Action)
		assert.Equal(t, actorID, events[0].ActorID)
		// jsonb round-trips through Postgres reformatted (spaces after
		// ':'/',' -- not the compact form json.Marshal produced when this
		// was written), so parse it back rather than substring-matching
		// the raw bytes.
		var diff struct {
			To struct {
				AlertRetentionMonths    int `json:"alertRetentionMonths"`
				IncidentRetentionMonths int `json:"incidentRetentionMonths"`
			} `json:"to"`
		}
		require.NoError(t, json.Unmarshal(events[0].Data, &diff))
		assert.Equal(t, 3, diff.To.AlertRetentionMonths)
		assert.Equal(t, 9, diff.To.IncidentRetentionMonths)
	})
}
