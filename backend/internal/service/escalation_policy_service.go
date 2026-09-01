package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kuruops/kuruops/internal/httpguard"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/notifier"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
)

// escalationPolicyRepo is the subset of *repository.EscalationPolicyRepository
// this service calls -- an interface, not the concrete type, purely so
// tests can substitute a repo double that fails on demand to exercise the
// error-wrapping branches (a DB call failing mid-transaction) a real
// Postgres integration test can't trigger.
// *repository.EscalationPolicyRepository already satisfies this implicitly,
// so every existing constructor call site is unaffected.
type escalationPolicyRepo interface {
	List(ctx context.Context, tx pgx.Tx) ([]domain.EscalationPolicy, error)
	GetBySeverity(ctx context.Context, tx pgx.Tx, severity domain.Severity) (*domain.EscalationPolicy, error)
	Save(ctx context.Context, tx pgx.Tx, p *domain.EscalationPolicy) error
	Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
}

// EscalationPolicyService is Settings -> Escala de Acionamento: lets an
// admin configure, per severity, an ordered chain of steps for notifying
// whoever's on shift about an alert. cmd/worker's escalation sweep reads
// these policies+steps directly via SQL (cross-tenant, same BYPASSRLS
// reasoning as its materialized-view refresh / SLA sweep), not through this
// service -- ResolveStepNotification below is what AlertHandlers.escalate's
// manual-escalation path calls instead, since a single HTTP request is
// already tenant-scoped.
type EscalationPolicyService struct {
	pool      *db.Pool
	repo      escalationPolicyRepo
	schedules *repository.OnCallScheduleRepository
	onCall    *OnCallScheduleService
	users     *UserService
	secrets   secrets.Store
	audit     *repository.AdminAuditEventRepository
}

func NewEscalationPolicyService(pool *db.Pool, repo escalationPolicyRepo, schedules *repository.OnCallScheduleRepository, onCall *OnCallScheduleService, users *UserService, store secrets.Store, audit *repository.AdminAuditEventRepository) *EscalationPolicyService {
	return &EscalationPolicyService{pool: pool, repo: repo, schedules: schedules, onCall: onCall, users: users, secrets: store, audit: audit}
}

func escalationPolicyAuditFields(p *domain.EscalationPolicy) map[string]any {
	if p == nil {
		return nil
	}
	steps := make([]map[string]any, len(p.Steps))
	for i, st := range p.Steps {
		steps[i] = map[string]any{
			"scheduleId": st.ScheduleID, "delayMinutes": st.DelayMinutes, "channelType": st.ChannelType,
			"destinationSet": st.DestinationSecretRef != "", "webhookPayloadTemplateSet": st.WebhookPayloadTemplate != nil,
		}
	}
	return map[string]any{"severity": p.Severity, "steps": steps}
}

func (s *EscalationPolicyService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.EscalationPolicy, error) {
	var policies []domain.EscalationPolicy
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		policies = v
		return err
	})
	return policies, err
}

func (s *EscalationPolicyService) Get(ctx context.Context, tenantID uuid.UUID, severity domain.Severity) (*domain.EscalationPolicy, error) {
	var policy *domain.EscalationPolicy
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.GetBySeverity(ctx, tx, severity)
		policy = v
		return err
	})
	return policy, err
}

func validEscalationChannel(channelType domain.EscalationChannelType) bool {
	switch channelType {
	case domain.EscalationChannelPagerDuty, domain.EscalationChannelSlack, domain.EscalationChannelWebhook:
		return true
	default:
		return false
	}
}

// Save creates or replaces the chain of steps for in.Severity -- the whole
// chain is resubmitted and wholesale-replaced on every save, same idiom
// PlaybookService.Save uses for a playbook's steps. Each step's Destination
// is plaintext; "" for a given position means keep that position's existing
// secret (matches the position against the chain currently saved for this
// severity, same "" -> keep-existing convention every other stored
// credential in this app uses) -- a brand-new position (the chain grew)
// requires a non-empty destination.
func (s *EscalationPolicyService) Save(ctx context.Context, tenantID, actorID uuid.UUID, in domain.SaveEscalationPolicyInput) (*domain.EscalationPolicy, error) {
	if len(in.Steps) == 0 {
		return nil, fmt.Errorf("an escalation chain needs at least one step")
	}
	for i, st := range in.Steps {
		if st.DelayMinutes <= 0 {
			return nil, fmt.Errorf("step %d: delayMinutes must be greater than zero", i+1)
		}
		if !validEscalationChannel(st.ChannelType) {
			return nil, fmt.Errorf("step %d: unknown channel type %q", i+1, st.ChannelType)
		}
		if st.ChannelType == domain.EscalationChannelWebhook && st.Destination != "" {
			// Same "catch it at save time" reasoning as the template check
			// below. Advisory only -- httpguard's dial-time check is the
			// actual control (see PreflightURL) -- but it turns a
			// destination that could never work into a form error instead
			// of a failed step discovered mid-incident.
			if err := httpguard.PreflightURL(st.Destination); err != nil {
				return nil, fmt.Errorf("step %d: destination %v", i+1, err)
			}
		}
		if st.ChannelType == domain.EscalationChannelWebhook && st.WebhookPayloadTemplate != "" {
			// Catch a malformed template at save time (e.g. a stray brace)
			// rather than the first time it's actually sent -- render it
			// against sample values and confirm the result is valid JSON.
			sample := notifier.RenderWebhookTemplate(st.WebhookPayloadTemplate, notifier.Notification{
				Title: "Sample Alert", Description: "Sample description", Severity: string(in.Severity),
				AlertID: uuid.New().String(), URL: "https://example.invalid/alerts/sample",
				AnalystName: "Sample Analyst", AnalystEmail: "analyst@example.invalid", AnalystPhone: "+1 555-0100",
			})
			if !json.Valid([]byte(sample)) {
				return nil, fmt.Errorf("step %d: webhook payload template does not produce valid JSON", i+1)
			}
		}
	}

	p := &domain.EscalationPolicy{TenantID: tenantID, Severity: in.Severity}

	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.GetBySeverity(ctx, tx, in.Severity)
		if err != nil {
			return fmt.Errorf("load existing chain: %w", err)
		}

		steps := make([]domain.EscalationStep, len(in.Steps))
		for i, st := range in.Steps {
			sched, err := s.schedules.Get(ctx, tx, st.ScheduleID)
			if err != nil {
				return fmt.Errorf("step %d: load schedule: %w", i+1, err)
			}
			if sched == nil {
				return fmt.Errorf("step %d: on-call schedule not found", i+1)
			}

			existingRef := ""
			if existing != nil && i < len(existing.Steps) {
				existingRef = existing.Steps[i].DestinationSecretRef
			}
			ref, err := secrets.PutOrKeepExisting(ctx, s.secrets, tenantID.String(), fmt.Sprintf("escalation:%s:%d", in.Severity, i), st.Destination, existingRef)
			if err != nil {
				return fmt.Errorf("step %d: store destination: %w", i+1, err)
			}
			if ref == "" {
				return fmt.Errorf("step %d: destination is required", i+1)
			}

			step := domain.EscalationStep{
				ScheduleID: st.ScheduleID, DelayMinutes: st.DelayMinutes,
				ChannelType: st.ChannelType, DestinationSecretRef: ref,
			}
			if st.ChannelType == domain.EscalationChannelWebhook && st.WebhookPayloadTemplate != "" {
				step.WebhookPayloadTemplate = &st.WebhookPayloadTemplate
			}
			steps[i] = step
		}
		p.Steps = steps

		if err := s.repo.Save(ctx, tx, p); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": escalationPolicyAuditFields(existing), "to": escalationPolicyAuditFields(p)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "escalation-policy", Action: "save", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("save escalation chain: %w", err)
	}
	return s.Get(ctx, tenantID, in.Severity)
}

// Test sends a real notification through step stepPosition (0-indexed) of
// the already-saved chain for severity, using its resolved destination
// secret (and, for webhook, its saved payload template) -- lets an admin
// confirm delivery actually works without waiting for a real alert to
// escalate.
func (s *EscalationPolicyService) Test(ctx context.Context, tenantID uuid.UUID, severity domain.Severity, stepPosition int) error {
	p, err := s.Get(ctx, tenantID, severity)
	if err != nil {
		return fmt.Errorf("load escalation chain: %w", err)
	}
	if p == nil || stepPosition < 0 || stepPosition >= len(p.Steps) {
		return fmt.Errorf("no such step configured for severity %q", severity)
	}
	step := p.Steps[stepPosition]

	notification, destination, err := s.ResolveStepNotification(ctx, tenantID, step, notifier.Notification{
		Title:       "Test escalation from KuruOps",
		Description: "This is a test notification triggered from Settings -> Escala de Acionamento.",
		Severity:    string(severity),
		AlertID:     uuid.Nil.String(),
	})
	if err != nil {
		return err
	}

	sender, err := notifier.NewForPolicy(string(step.ChannelType), step.WebhookPayloadTemplate)
	if err != nil {
		return err
	}
	return sender.Send(ctx, destination, notification)
}

// ResolveStepNotification fills in base's analyst placeholders by resolving
// who's on call right now against step's OnCallSchedule (not necessarily
// the tenant's default), and resolves step's destination secret. Used by
// cmd/worker's sweepEscalations (automatic SLA loop) and
// AlertHandlers.escalate (manual escalation, fires the next step once) --
// both already have a resolved *domain.EscalationStep in hand and just need
// a ready-to-send notifier.Notification + destination.
func (s *EscalationPolicyService) ResolveStepNotification(ctx context.Context, tenantID uuid.UUID, step domain.EscalationStep, base notifier.Notification) (notifier.Notification, string, error) {
	n := base
	analystID, err := s.onCall.ResolveAnalystForSchedule(ctx, tenantID, step.ScheduleID, time.Now())
	if err != nil {
		return notifier.Notification{}, "", fmt.Errorf("resolve on-call analyst: %w", err)
	}
	if analystID != nil {
		u, err := s.users.Get(ctx, tenantID, *analystID)
		if err != nil {
			return notifier.Notification{}, "", fmt.Errorf("load on-call analyst: %w", err)
		}
		if u != nil {
			n.AnalystName = u.Name
			n.AnalystEmail = u.Email
			if u.Phone != nil {
				n.AnalystPhone = *u.Phone
			}
		}
	}

	destination, err := s.secrets.Resolve(ctx, step.DestinationSecretRef)
	if err != nil {
		return notifier.Notification{}, "", fmt.Errorf("resolve destination: %w", err)
	}
	return n, destination, nil
}

func (s *EscalationPolicyService) Delete(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.Delete(ctx, tx, id); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": map[string]any{"id": id}, "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "escalation-policy", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}
