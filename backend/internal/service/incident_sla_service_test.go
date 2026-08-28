package service_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/testutil"
)

func TestIncidentSLAService_Save(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	auditRepo := repository.NewAdminAuditEventRepository()
	svc := service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), auditRepo)

	t.Run("rejects a non-positive due-within value", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SeverityCritical, domain.PriorityP1, 0)
		assert.ErrorContains(t, err, "greater than zero")
	})

	policy, err := svc.Save(t.Context(), tenantID, actorID, domain.SeverityCritical, domain.PriorityP1, 60)
	require.NoError(t, err)
	assert.Equal(t, 60, policy.DueWithinMinutes)

	list, err := svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, svc.Delete(t.Context(), tenantID, actorID, policy.ID))
	list, err = svc.List(t.Context(), tenantID)
	require.NoError(t, err)
	assert.Empty(t, list)

	t.Run("save and delete each record an admin audit event", func(t *testing.T) {
		tx := testutil.BeginTx(t, pool, tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 10)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			assert.Equal(t, "incident-sla", e.Area)
			actions = append(actions, e.Action)
		}
		assert.Contains(t, actions, "save")
		assert.Contains(t, actions, "delete")
	})
}

func TestIncidentSLAService_DueAt(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	svc := service.NewIncidentSLAService(pool, repository.NewIncidentSLARepository(), repository.NewAdminAuditEventRepository())

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

	_, err := svc.Save(t.Context(), tenantID, actorID, domain.SeverityCritical, domain.PriorityP1, 30)
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
