package service_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestIncidentSLAService_Save(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository())

	t.Run("rejects a non-positive due-within value", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, domain.PriorityP1, 0)
		assert.ErrorContains(t, err, "greater than zero")
	})

	policy, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, domain.PriorityP1, 60)
	require.NoError(t, err)
	assert.Equal(t, 60, policy.DueWithinMinutes)

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, svc.Delete(t.Context(), tenantID, policy.ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestIncidentSLAService_DueAt(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	svc := service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository())

	t.Run("unconfigured pair returns nil", func(t *testing.T) {
		var due *time.Time
		err := pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			d, err := svc.DueAt(t.Context(), tx, domain.SeverityLow, domain.PriorityP4)
			due = d
			return err
		})
		require.NoError(t, err)
		assert.Nil(t, due)
	})

	_, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, domain.PriorityP1, 30)
	require.NoError(t, err)

	t.Run("configured pair returns roughly now + due-within minutes", func(t *testing.T) {
		var due *time.Time
		err := pool.WithTenant(t.Context(), tenantID, func(tx pgx.Tx) error {
			d, err := svc.DueAt(t.Context(), tx, domain.SeverityCritical, domain.PriorityP1)
			due = d
			return err
		})
		require.NoError(t, err)
		require.NotNil(t, due)
		assert.WithinDuration(t, time.Now().Add(30*time.Minute), *due, 5*time.Second)
	})
}
