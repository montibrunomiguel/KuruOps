package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

func TestEscalationPolicyService_Save(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	store := secrets.NewEnvStore()
	svc := service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), store)

	t.Run("rejects a non-positive threshold", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, 0, domain.EscalationChannelPagerDuty, "routing-key")
		assert.ErrorContains(t, err, "greater than zero")
	})

	t.Run("rejects an unknown channel type", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, 15, "carrier-pigeon", "dest")
		assert.ErrorContains(t, err, "unknown channel type")
	})

	t.Run("initial save requires a destination", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, domain.SeverityHigh, 15, domain.EscalationChannelSlack, "")
		assert.ErrorContains(t, err, "destination is required")
	})

	policy, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, 15, domain.EscalationChannelPagerDuty, "R0UTING-KEY")
	require.NoError(t, err)
	assert.Equal(t, 15, policy.UnacknowledgedAfterMinutes)
	assert.Equal(t, domain.EscalationChannelPagerDuty, policy.ChannelType)

	resolved, err := store.Resolve(t.Context(), policy.DestinationSecretRef)
	require.NoError(t, err)
	assert.Equal(t, "R0UTING-KEY", resolved)

	t.Run("blank destination on update keeps the existing one", func(t *testing.T) {
		updated, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, 30, domain.EscalationChannelPagerDuty, "")
		require.NoError(t, err)
		assert.Equal(t, 30, updated.UnacknowledgedAfterMinutes)

		resolved, err := store.Resolve(t.Context(), updated.DestinationSecretRef)
		require.NoError(t, err)
		assert.Equal(t, "R0UTING-KEY", resolved, "the original destination must survive an update that doesn't supply a new one")
	})

	t.Run("a non-blank destination on update replaces the stored secret", func(t *testing.T) {
		updated, err := svc.Save(t.Context(), tenantID, domain.SeverityCritical, 30, domain.EscalationChannelPagerDuty, "NEW-ROUTING-KEY")
		require.NoError(t, err)

		resolved, err := store.Resolve(t.Context(), updated.DestinationSecretRef)
		require.NoError(t, err)
		assert.Equal(t, "NEW-ROUTING-KEY", resolved)
	})

	t.Run("List returns every configured policy", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, domain.SeverityHigh, 30, domain.EscalationChannelSlack, "https://hooks.slack.example/x")
		require.NoError(t, err)

		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("Delete removes a policy", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID, policy.ID))
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		for _, p := range list {
			assert.NotEqual(t, policy.ID, p.ID)
		}
	})
}
