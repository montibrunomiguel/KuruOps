package repository_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/testutil"
)

// newEscalationTestSchedule inserts an on-call schedule through the SAME tx
// the calling test uses for everything else -- BeginTx's transaction is
// never committed (only rolled back at cleanup, see its doc comment), so a
// schedule inserted through a second, separate BeginTx would be invisible
// to the first tx's later inserts, tripping escalation_policy_steps'
// schedule_id foreign key.
func newEscalationTestSchedule(t *testing.T, tx pgx.Tx, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	sched := &domain.OnCallSchedule{
		TenantID: tenantID, Name: "Primary", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1,
		WorkingHoursMode: domain.OnCallWorkingHoursAllDay, Participants: []domain.OnCallParticipant{}, WorkingHours: []domain.OnCallWorkingHoursInterval{},
	}
	require.NoError(t, repository.NewOnCallScheduleRepository().Insert(t.Context(), tx, sched))
	return sched.ID
}

func TestEscalationPolicyRepository(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	repo := repository.NewEscalationPolicyRepository()
	tx := testutil.BeginTx(t, pool, tenantID)

	scheduleID := newEscalationTestSchedule(t, tx, tenantID)

	t.Run("GetBySeverity for an unconfigured severity returns nil, not an error", func(t *testing.T) {
		got, err := repo.GetBySeverity(t.Context(), tx, domain.SeverityCritical)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	p := &domain.EscalationPolicy{
		TenantID: tenantID, Severity: domain.SeverityCritical,
		Steps: []domain.EscalationStep{
			{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelPagerDuty, DestinationSecretRef: "tenant/escalation:critical:0"},
			{ScheduleID: scheduleID, DelayMinutes: 30, ChannelType: domain.EscalationChannelSlack, DestinationSecretRef: "tenant/escalation:critical:1"},
		},
	}
	require.NoError(t, repo.Save(t.Context(), tx, p))
	require.NotEqual(t, "", p.ID.String())

	t.Run("GetBySeverity after save returns steps ordered by position, with denormalized schedule names", func(t *testing.T) {
		got, err := repo.GetBySeverity(t.Context(), tx, domain.SeverityCritical)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Len(t, got.Steps, 2)
		assert.Equal(t, 0, got.Steps[0].Position)
		assert.Equal(t, 15, got.Steps[0].DelayMinutes)
		assert.Equal(t, domain.EscalationChannelPagerDuty, got.Steps[0].ChannelType)
		assert.Equal(t, "Primary", got.Steps[0].ScheduleName)
		assert.Equal(t, 1, got.Steps[1].Position)
		assert.Equal(t, 30, got.Steps[1].DelayMinutes)
	})

	t.Run("saving the same severity again wholesale-replaces its steps, doesn't insert a second policy row", func(t *testing.T) {
		require.NoError(t, repo.Save(t.Context(), tx, &domain.EscalationPolicy{
			ID: p.ID, TenantID: tenantID, Severity: domain.SeverityCritical,
			Steps: []domain.EscalationStep{
				{ScheduleID: scheduleID, DelayMinutes: 5, ChannelType: domain.EscalationChannelWebhook, DestinationSecretRef: "tenant/escalation:critical-v2:0"},
			},
		}))
		got, err := repo.GetBySeverity(t.Context(), tx, domain.SeverityCritical)
		require.NoError(t, err)
		require.Len(t, got.Steps, 1)
		assert.Equal(t, 5, got.Steps[0].DelayMinutes)
		assert.Equal(t, domain.EscalationChannelWebhook, got.Steps[0].ChannelType)
		assert.Equal(t, p.ID, got.ID)
	})

	t.Run("List returns every configured policy with its steps", func(t *testing.T) {
		require.NoError(t, repo.Save(t.Context(), tx, &domain.EscalationPolicy{
			TenantID: tenantID, Severity: domain.SeverityHigh,
			Steps: []domain.EscalationStep{
				{ScheduleID: scheduleID, DelayMinutes: 30, ChannelType: domain.EscalationChannelWebhook, DestinationSecretRef: "tenant/escalation:high:0"},
			},
		}))
		policies, err := repo.List(t.Context(), tx)
		require.NoError(t, err)
		require.Len(t, policies, 2)
		for _, policy := range policies {
			assert.NotEmpty(t, policy.Steps)
		}
	})

	t.Run("Delete removes a policy and cascades its steps", func(t *testing.T) {
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
	scheduleA := newEscalationTestSchedule(t, txA, tenantA)
	require.NoError(t, repo.Save(t.Context(), txA, &domain.EscalationPolicy{
		TenantID: tenantA, Severity: domain.SeverityCritical,
		Steps: []domain.EscalationStep{{ScheduleID: scheduleA, DelayMinutes: 15, ChannelType: domain.EscalationChannelPagerDuty, DestinationSecretRef: "ref"}},
	}))

	txB := testutil.BeginTx(t, pool, tenantB)
	got, err := repo.GetBySeverity(t.Context(), txB, domain.SeverityCritical)
	require.NoError(t, err)
	assert.Nil(t, got, "RLS must prevent tenant B from seeing tenant A's escalation policy")
}
