package repository_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

func TestEscalationPolicyRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewEscalationPolicyRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	t.Run("GetBySeverity for an unconfigured severity returns nil, not an error", func(t *testing.T) {
		got, err := repo.GetBySeverity(t.Context(), tx, domain.SeverityCritical)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	p := &domain.EscalationPolicy{
		TenantID: tenantID, Severity: domain.SeverityCritical, UnacknowledgedAfterMinutes: 15,
		ChannelType: domain.EscalationChannelPagerDuty, DestinationSecretRef: "tenant/escalation:critical",
	}
	require.NoError(t, repo.Upsert(t.Context(), tx, p))
	require.NotEqual(t, p.ID.String(), "")

	t.Run("GetBySeverity after upsert", func(t *testing.T) {
		got, err := repo.GetBySeverity(t.Context(), tx, domain.SeverityCritical)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 15, got.UnacknowledgedAfterMinutes)
		assert.Equal(t, domain.EscalationChannelPagerDuty, got.ChannelType)
	})

	t.Run("upserting the same severity again replaces the row, doesn't insert a second one", func(t *testing.T) {
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.EscalationPolicy{
			TenantID: tenantID, Severity: domain.SeverityCritical, UnacknowledgedAfterMinutes: 5,
			ChannelType: domain.EscalationChannelSlack, DestinationSecretRef: "tenant/escalation:critical-v2",
		}))
		got, err := repo.GetBySeverity(t.Context(), tx, domain.SeverityCritical)
		require.NoError(t, err)
		assert.Equal(t, 5, got.UnacknowledgedAfterMinutes)
		assert.Equal(t, domain.EscalationChannelSlack, got.ChannelType)
		assert.Equal(t, p.ID, got.ID)
	})

	t.Run("List returns every configured policy", func(t *testing.T) {
		require.NoError(t, repo.Upsert(t.Context(), tx, &domain.EscalationPolicy{
			TenantID: tenantID, Severity: domain.SeverityHigh, UnacknowledgedAfterMinutes: 30,
			ChannelType: domain.EscalationChannelWebhook, DestinationSecretRef: "tenant/escalation:high",
		}))
		policies, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		assert.Len(t, policies, 2)
	})

	t.Run("Delete removes a policy", func(t *testing.T) {
		require.NoError(t, repo.Delete(t.Context(), tx, p.ID))
		got, err := repo.GetBySeverity(t.Context(), tx, domain.SeverityCritical)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestEscalationPolicyRepository_TenantIsolation(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantA := testutil.NewTenant(t)
	tenantB := testutil.NewTenant(t)
	repo := repository.NewEscalationPolicyRepository()

	txA := testutil.BeginTx(t, pool, tenantA)
	require.NoError(t, repo.Upsert(t.Context(), txA, &domain.EscalationPolicy{
		TenantID: tenantA, Severity: domain.SeverityCritical, UnacknowledgedAfterMinutes: 15,
		ChannelType: domain.EscalationChannelPagerDuty, DestinationSecretRef: "ref",
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.GetBySeverity(t.Context(), txB, domain.SeverityCritical)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's escalation policy")
}
