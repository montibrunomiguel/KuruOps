package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/notifier"
	"github.com/kuruops/kuruops/internal/repository"
)

type PlaybookService struct {
	pool       *db.Pool
	repo       *repository.PlaybookRepository
	alerts     *repository.AlertRepository
	appBaseURL string
}

func NewPlaybookService(pool *db.Pool, repo *repository.PlaybookRepository, alerts *repository.AlertRepository, appBaseURL string) *PlaybookService {
	return &PlaybookService{pool: pool, repo: repo, alerts: alerts, appBaseURL: appBaseURL}
}

func (s *PlaybookService) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.Playbook, error) {
	var pb *domain.Playbook
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.Get(ctx, tx, id)
		pb = v
		return err
	})
	return pb, err
}

func (s *PlaybookService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Playbook, error) {
	var playbooks []domain.Playbook
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		playbooks = v
		return err
	})
	return playbooks, err
}

func toDomainSteps(in map[domain.IncidentPhase][]domain.SavePlaybookStepInput) map[domain.IncidentPhase][]domain.PlaybookStep {
	out := make(map[domain.IncidentPhase][]domain.PlaybookStep, len(in))
	for phase, steps := range in {
		converted := make([]domain.PlaybookStep, len(steps))
		for i, step := range steps {
			converted[i] = domain.PlaybookStep{
				Text:                   step.Text,
				WebhookURL:             step.WebhookURL,
				WebhookPayloadTemplate: step.WebhookPayloadTemplate,
			}
		}
		out[phase] = converted
	}
	return out
}

func (s *PlaybookService) Create(ctx context.Context, tenantID, actorID uuid.UUID, in domain.SavePlaybookInput) (*domain.Playbook, error) {
	pb := &domain.Playbook{
		TenantID:         tenantID,
		Title:            in.Title,
		Category:         in.Category,
		Description:      in.Description,
		Keywords:         orEmptySlice(in.Keywords), // playbooks.keywords is NOT NULL
		AlertNamePattern: in.AlertNamePattern,
		IsDefault:        in.IsDefault,
		Steps:            toDomainSteps(in.Steps),
		CreatedBy:        &actorID,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, pb)
	})
	if err != nil {
		return nil, fmt.Errorf("create playbook: %w", err)
	}
	return pb, nil
}

func (s *PlaybookService) Update(ctx context.Context, tenantID, id uuid.UUID, in domain.SavePlaybookInput) (*domain.Playbook, error) {
	pb := &domain.Playbook{
		ID:               id,
		TenantID:         tenantID,
		Title:            in.Title,
		Category:         in.Category,
		Description:      in.Description,
		Keywords:         orEmptySlice(in.Keywords), // playbooks.keywords is NOT NULL
		AlertNamePattern: in.AlertNamePattern,
		IsDefault:        in.IsDefault,
		Steps:            toDomainSteps(in.Steps),
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Update(ctx, tx, pb)
	})
	if err != nil {
		return nil, fmt.Errorf("update playbook: %w", err)
	}
	return pb, nil
}

func (s *PlaybookService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
}

// MatchForAlertTitle picks the playbook to show as "Related Playbook" on
// Alert Detail for a title not already backed by a stored Alert.PlaybookID
// (e.g. previewing what a not-yet-saved alert would get) -- the actual
// per-alert assignment happens once, at ingest time, in AlertService.Ingest.
// Thin wrapper: the matching logic itself (most specific alert_name_pattern,
// falling back to the tenant's is_default playbook) lives in SQL, see
// PlaybookRepository.MatchForAlertTitle.
func (s *PlaybookService) MatchForAlertTitle(ctx context.Context, tenantID uuid.UUID, alertTitle string) (*domain.Playbook, error) {
	var pb *domain.Playbook
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.MatchForAlertTitle(ctx, tx, alertTitle)
		pb = v
		return err
	})
	return pb, err
}

// TriggerStepWebhook fires the real outbound POST configured on a
// containment (or any other phase's) step, against alertID's own data --
// same payload-template mechanism (RenderWebhookTemplate/WebhookPlaceholders)
// already used for escalation policy webhooks, so a tenant reuses the exact
// {{title}}/{{severity}}/etc. placeholders they already know. Every attempt,
// success or failure, is recorded as an AlertEventPlaybookWebhookTriggered
// event on the alert -- an audit trail of which automations actually ran.
func (s *PlaybookService) TriggerStepWebhook(ctx context.Context, tenantID, actorID, stepID, alertID uuid.UUID) error {
	// sendErr is captured outside the transaction closure rather than
	// returned from it: WithTenant rolls back the whole transaction on any
	// non-nil error, which would silently discard the very
	// AlertEventPlaybookWebhookTriggered row this method exists to
	// guarantee -- a failed send must still commit its audit trail.
	var sendErr error
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		playbookID, url, template, found, err := s.repo.GetStepWebhookConfig(ctx, tx, stepID)
		if err != nil {
			return fmt.Errorf("load step webhook config: %w", err)
		}
		if !found {
			return fmt.Errorf("step %s has no webhook configured", stepID)
		}

		alert, err := s.alerts.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if alert == nil {
			return fmt.Errorf("alert %s not found", alertID)
		}

		notification := notifier.Notification{
			Title:       alert.Title,
			Description: alert.Source,
			Severity:    string(alert.Severity),
			AlertID:     alert.ID.String(),
			URL:         s.appBaseURL + "/alerts/" + alert.ID.String(),
		}
		sender, err := notifier.NewForPolicy("webhook", &template)
		if err != nil {
			return err
		}
		sendErr = sender.Send(ctx, url, notification)

		eventData, _ := json.Marshal(map[string]any{
			"playbookId": playbookID,
			"stepId":     stepID,
			"success":    sendErr == nil,
		})
		if err := s.alerts.InsertEvent(ctx, tx, &domain.AlertEvent{
			AlertID:   alertID,
			TenantID:  tenantID,
			EventType: domain.AlertEventPlaybookWebhookTriggered,
			ActorType: domain.ActorUser,
			ActorID:   &actorID,
			Data:      eventData,
		}); err != nil {
			return fmt.Errorf("record webhook trigger event: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if sendErr != nil {
		return fmt.Errorf("send webhook: %w", sendErr)
	}
	return nil
}
