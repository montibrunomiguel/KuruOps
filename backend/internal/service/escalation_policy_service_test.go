package service_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/notifier"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
	"github.com/argusops/argusops/internal/service"
	"github.com/argusops/argusops/internal/testutil"
)

// fakeEscalationPolicyRepo lets a test fail a specific repo call on demand
// -- EscalationPolicyService takes an interface (not the concrete
// *repository.EscalationPolicyRepository) specifically so this is possible.
// See fakeStorageConfigRepo (storage_config_service_test.go) for the fuller
// version of this reasoning.
type fakeEscalationPolicyRepo struct {
	getBySeverityErr error
	saveErr          error
	deleteErr        error
}

func (f *fakeEscalationPolicyRepo) List(context.Context, pgx.Tx) ([]domain.EscalationPolicy, error) {
	return nil, nil
}
func (f *fakeEscalationPolicyRepo) GetBySeverity(context.Context, pgx.Tx, domain.Severity) (*domain.EscalationPolicy, error) {
	return nil, f.getBySeverityErr
}
func (f *fakeEscalationPolicyRepo) Save(context.Context, pgx.Tx, *domain.EscalationPolicy) error {
	return f.saveErr
}
func (f *fakeEscalationPolicyRepo) Delete(context.Context, pgx.Tx, uuid.UUID) error {
	return f.deleteErr
}

func newEscalationPolicyServiceFixture(t *testing.T) (svc *service.EscalationPolicyService, store *secrets.EnvStore, onCallSvc *service.OnCallScheduleService, tenantID, scheduleID, actorID uuid.UUID) {
	t.Helper()
	pool := testutil.RequireTestDB(t)
	tenantID = testutil.NewTenant(t)
	store = secrets.NewEnvStore()

	actorID = testutil.NewUser(t, tenantID, "admin", nil)
	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallSvc = service.NewOnCallScheduleService(pool, onCallScheduleRepo, repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	sched, err := onCallSvc.Create(t.Context(), tenantID, actorID, domain.SaveOnCallScheduleInput{
		Name: "Primary", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	})
	require.NoError(t, err)
	scheduleID = sched.ID

	userSvc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())
	svc = service.NewEscalationPolicyService(pool, repository.NewEscalationPolicyRepository(), onCallScheduleRepo, onCallSvc, userSvc, store, repository.NewAdminAuditEventRepository())
	return svc, store, onCallSvc, tenantID, scheduleID, actorID
}

func TestEscalationPolicyService_Save(t *testing.T) {
	svc, store, _, tenantID, scheduleID, actorID := newEscalationPolicyServiceFixture(t)

	t.Run("rejects a chain with no steps", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{Severity: domain.SeverityCritical})
		assert.ErrorContains(t, err, "at least one step")
	})

	t.Run("rejects a non-positive delay", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityCritical,
			Steps:    []domain.SaveEscalationStepInput{{ScheduleID: scheduleID, DelayMinutes: 0, ChannelType: domain.EscalationChannelPagerDuty, Destination: "routing-key"}},
		})
		assert.ErrorContains(t, err, "greater than zero")
	})

	t.Run("rejects an unknown channel type", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityCritical,
			Steps:    []domain.SaveEscalationStepInput{{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: "carrier-pigeon", Destination: "dest"}},
		})
		assert.ErrorContains(t, err, "unknown channel type")
	})

	t.Run("rejects a step whose schedule doesn't exist", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityCritical,
			Steps:    []domain.SaveEscalationStepInput{{ScheduleID: uuid.New(), DelayMinutes: 15, ChannelType: domain.EscalationChannelPagerDuty, Destination: "dest"}},
		})
		assert.ErrorContains(t, err, "not found")
	})

	t.Run("initial save requires a destination", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityHigh,
			Steps:    []domain.SaveEscalationStepInput{{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelSlack}},
		})
		assert.ErrorContains(t, err, "destination is required")
	})

	policy, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
		Severity: domain.SeverityCritical,
		Steps: []domain.SaveEscalationStepInput{
			{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelPagerDuty, Destination: "R0UTING-KEY"},
			{ScheduleID: scheduleID, DelayMinutes: 30, ChannelType: domain.EscalationChannelSlack, Destination: "https://hooks.slack.example/x"},
		},
	})
	require.NoError(t, err)
	require.Len(t, policy.Steps, 2)
	assert.Equal(t, 15, policy.Steps[0].DelayMinutes)
	assert.Equal(t, domain.EscalationChannelPagerDuty, policy.Steps[0].ChannelType)
	assert.Equal(t, 0, policy.Steps[0].Position)
	assert.Equal(t, 1, policy.Steps[1].Position)

	resolved, err := store.Resolve(t.Context(), policy.Steps[0].DestinationSecretRef)
	require.NoError(t, err)
	assert.Equal(t, "R0UTING-KEY", resolved)

	t.Run("blank destination on update keeps that position's existing secret", func(t *testing.T) {
		updated, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityCritical,
			Steps: []domain.SaveEscalationStepInput{
				{ScheduleID: scheduleID, DelayMinutes: 20, ChannelType: domain.EscalationChannelPagerDuty},
				{ScheduleID: scheduleID, DelayMinutes: 30, ChannelType: domain.EscalationChannelSlack},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 20, updated.Steps[0].DelayMinutes)

		resolved, err := store.Resolve(t.Context(), updated.Steps[0].DestinationSecretRef)
		require.NoError(t, err)
		assert.Equal(t, "R0UTING-KEY", resolved, "the original destination must survive an update that doesn't supply a new one")
	})

	t.Run("a non-blank destination on update replaces the stored secret", func(t *testing.T) {
		updated, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityCritical,
			Steps: []domain.SaveEscalationStepInput{
				{ScheduleID: scheduleID, DelayMinutes: 20, ChannelType: domain.EscalationChannelPagerDuty, Destination: "NEW-ROUTING-KEY"},
				{ScheduleID: scheduleID, DelayMinutes: 30, ChannelType: domain.EscalationChannelSlack},
			},
		})
		require.NoError(t, err)

		resolved, err := store.Resolve(t.Context(), updated.Steps[0].DestinationSecretRef)
		require.NoError(t, err)
		assert.Equal(t, "NEW-ROUTING-KEY", resolved)
	})

	t.Run("a brand-new position with a blank destination is rejected", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityCritical,
			Steps: []domain.SaveEscalationStepInput{
				{ScheduleID: scheduleID, DelayMinutes: 20, ChannelType: domain.EscalationChannelPagerDuty},
				{ScheduleID: scheduleID, DelayMinutes: 30, ChannelType: domain.EscalationChannelSlack},
				{ScheduleID: scheduleID, DelayMinutes: 45, ChannelType: domain.EscalationChannelWebhook},
			},
		})
		assert.ErrorContains(t, err, "destination is required")
	})

	t.Run("List returns every configured policy", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityHigh,
			Steps:    []domain.SaveEscalationStepInput{{ScheduleID: scheduleID, DelayMinutes: 30, ChannelType: domain.EscalationChannelSlack, Destination: "https://hooks.slack.example/y"}},
		})
		require.NoError(t, err)

		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		assert.Len(t, list, 2)
	})

	t.Run("Delete removes a policy", func(t *testing.T) {
		require.NoError(t, svc.Delete(t.Context(), tenantID, actorID, policy.ID))
		list, err := svc.List(t.Context(), tenantID)
		require.NoError(t, err)
		for _, p := range list {
			assert.NotEqual(t, policy.ID, p.ID)
		}
	})

	t.Run("save and delete each record an admin audit event", func(t *testing.T) {
		auditRepo := repository.NewAdminAuditEventRepository()
		tx := testutil.BeginTx(t, testutil.RequireTestDB(t), tenantID)
		events, err := auditRepo.List(t.Context(), tx, nil, 20)
		require.NoError(t, err)
		var actions []string
		for _, e := range events {
			if e.Area == "escalation-policy" {
				actions = append(actions, e.Action)
			}
		}
		assert.Contains(t, actions, "save")
		assert.Contains(t, actions, "delete")
	})
}

func TestEscalationPolicyService_WebhookPayloadTemplate(t *testing.T) {
	svc, _, _, tenantID, scheduleID, actorID := newEscalationPolicyServiceFixture(t)

	t.Run("rejects a template that doesn't render to valid JSON", func(t *testing.T) {
		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityLow,
			Steps: []domain.SaveEscalationStepInput{
				{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelWebhook, Destination: "https://hook.example/x", WebhookPayloadTemplate: `{"title": {{title}}`},
			},
		})
		assert.ErrorContains(t, err, "valid JSON")
	})

	t.Run("accepts and stores a valid custom template with analyst placeholders", func(t *testing.T) {
		policy, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityMedium,
			Steps: []domain.SaveEscalationStepInput{
				{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelWebhook, Destination: "https://hook.example/x", WebhookPayloadTemplate: `{"text": "{{severity}}: {{title}} -- {{analystName}} ({{analystPhone}})"}`},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, policy.Steps[0].WebhookPayloadTemplate)
		assert.Equal(t, `{"text": "{{severity}}: {{title}} -- {{analystName}} ({{analystPhone}})"}`, *policy.Steps[0].WebhookPayloadTemplate)
	})

	t.Run("a non-webhook channel ignores the template even if one is supplied", func(t *testing.T) {
		policy, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityInformational,
			Steps: []domain.SaveEscalationStepInput{
				{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelSlack, Destination: "https://hooks.slack.example/y", WebhookPayloadTemplate: `{"text": "ignored"}`},
			},
		})
		require.NoError(t, err)
		assert.Nil(t, policy.Steps[0].WebhookPayloadTemplate)
	})
}

func TestEscalationPolicyService_Test(t *testing.T) {
	svc, _, _, tenantID, scheduleID, actorID := newEscalationPolicyServiceFixture(t)

	t.Run("errors when no policy is configured for the severity", func(t *testing.T) {
		err := svc.Test(t.Context(), tenantID, domain.SeverityCritical, 0)
		assert.ErrorContains(t, err, "no such step configured")
	})

	t.Run("sends a real notification through the saved channel, using the saved template", func(t *testing.T) {
		var received string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			received = string(body)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		_, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
			Severity: domain.SeverityHigh,
			Steps: []domain.SaveEscalationStepInput{
				{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelWebhook, Destination: srv.URL, WebhookPayloadTemplate: `{"custom": "{{severity}}"}`},
			},
		})
		require.NoError(t, err)

		require.NoError(t, svc.Test(t.Context(), tenantID, domain.SeverityHigh, 0))
		assert.JSONEq(t, `{"custom": "high"}`, received)
	})

	t.Run("an out-of-range step position errors", func(t *testing.T) {
		err := svc.Test(t.Context(), tenantID, domain.SeverityHigh, 5)
		assert.ErrorContains(t, err, "no such step configured")
	})
}

func TestEscalationPolicyService_ResolveStepNotification(t *testing.T) {
	svc, _, onCallSvc, tenantID, scheduleID, actorID := newEscalationPolicyServiceFixture(t)

	policy, err := svc.Save(t.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{
		Severity: domain.SeverityCritical,
		Steps:    []domain.SaveEscalationStepInput{{ScheduleID: scheduleID, DelayMinutes: 15, ChannelType: domain.EscalationChannelWebhook, Destination: "https://hook.example/x"}},
	})
	require.NoError(t, err)

	base := notifier.Notification{Title: "Something bad", Severity: string(domain.SeverityCritical), AlertID: uuid.New().String()}

	t.Run("no analyst on shift leaves the placeholders empty", func(t *testing.T) {
		n, destination, err := svc.ResolveStepNotification(t.Context(), tenantID, policy.Steps[0], base)
		require.NoError(t, err)
		assert.Equal(t, "https://hook.example/x", destination)
		assert.Empty(t, n.AnalystEmail)
		assert.Empty(t, n.AnalystPhone)
	})

	t.Run("an analyst currently on shift fills in name/email", func(t *testing.T) {
		userID := testutil.NewUser(t, tenantID, "analyst", nil)
		actorID := testutil.NewUser(t, tenantID, "admin", nil)
		_, err := onCallSvc.Update(t.Context(), tenantID, actorID, scheduleID, domain.SaveOnCallScheduleInput{
			Name: "Primary", ParticipantIDs: []uuid.UUID{userID},
			HandoverAt: time.Now().Add(-24 * time.Hour), PeriodDays: 7, ConcurrentShifts: 1,
			WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
		})
		require.NoError(t, err)

		n, _, err := svc.ResolveStepNotification(t.Context(), tenantID, policy.Steps[0], base)
		require.NoError(t, err)
		assert.Equal(t, "Test User", n.AnalystName)
		assert.NotEmpty(t, n.AnalystEmail)
	})
}

// TestEscalationPolicyService_RepoErrors exercises Save/Delete's
// error-wrapping branches around the repo call that persists (Save also
// checks for an existing chain first) -- unreachable via a real Postgres
// integration test.
func TestEscalationPolicyService_RepoErrors(t *testing.T) {
	pool := testutil.RequireTestDB(t)
	tenantID := testutil.NewTenant(t)
	actorID := testutil.NewUser(t, tenantID, "admin", nil)
	store := secrets.NewEnvStore()

	onCallScheduleRepo := repository.NewOnCallScheduleRepository()
	onCallSvc := service.NewOnCallScheduleService(pool, onCallScheduleRepo, repository.NewUserRepository(), repository.NewTenantRepository(), repository.NewAdminAuditEventRepository())
	sched, err := onCallSvc.Create(t.Context(), tenantID, actorID, domain.SaveOnCallScheduleInput{
		Name: "Primary", HandoverAt: time.Now(), PeriodDays: 7, ConcurrentShifts: 1, WorkingHoursMode: domain.OnCallWorkingHoursAllDay,
	})
	require.NoError(t, err)
	userSvc := service.NewUserService(pool, repository.NewUserRepository(), repository.NewAdminAuditEventRepository())

	newSvc := func(fake *fakeEscalationPolicyRepo) *service.EscalationPolicyService {
		return service.NewEscalationPolicyService(pool, fake, onCallScheduleRepo, onCallSvc, userSvc, store, repository.NewAdminAuditEventRepository())
	}
	validInput := domain.SaveEscalationPolicyInput{
		Severity: domain.SeverityCritical,
		Steps:    []domain.SaveEscalationStepInput{{ScheduleID: sched.ID, DelayMinutes: 15, ChannelType: domain.EscalationChannelWebhook, Destination: "https://example.invalid"}},
	}

	t.Run("Save wraps a GetBySeverity failure", func(t *testing.T) {
		_, err := newSvc(&fakeEscalationPolicyRepo{getBySeverityErr: errors.New("get boom")}).Save(t.Context(), tenantID, actorID, validInput)
		assert.ErrorContains(t, err, "get boom")
	})

	t.Run("Save wraps a persist failure", func(t *testing.T) {
		_, err := newSvc(&fakeEscalationPolicyRepo{saveErr: errors.New("save boom")}).Save(t.Context(), tenantID, actorID, validInput)
		assert.ErrorContains(t, err, "save boom")
	})

	t.Run("Delete wraps a Delete failure", func(t *testing.T) {
		err := newSvc(&fakeEscalationPolicyRepo{deleteErr: errors.New("delete boom")}).Delete(t.Context(), tenantID, actorID, uuid.New())
		assert.ErrorContains(t, err, "delete boom")
	})
}
