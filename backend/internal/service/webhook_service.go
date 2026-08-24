package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// webhookRepo is the subset of *repository.WebhookRepository this service
// calls -- an interface, not the concrete type, purely so tests can
// substitute a repo double that fails on demand to exercise the
// error-wrapping branches (a DB call failing mid-transaction) a real
// Postgres integration test can't trigger. *repository.WebhookRepository
// already satisfies this implicitly, so every existing constructor call
// site is unaffected -- including cmd/ingest's Handler, which also depends
// on the concrete repository type directly; that's a separate field on a
// separate struct and is untouched.
type webhookRepo interface {
	List(ctx context.Context, tx pgx.Tx) ([]domain.WebhookEndpoint, error)
	Insert(ctx context.Context, tx pgx.Tx, ep *domain.WebhookEndpoint) error
	RotateToken(ctx context.Context, tx pgx.Tx, id uuid.UUID, tokenHash, tokenLast4 string, expiresAt *time.Time) error
	SetStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error
	SetFieldMappingTemplate(ctx context.Context, tx pgx.Tx, id uuid.UUID, templateID *uuid.UUID) error
	SetGroupByFields(ctx context.Context, tx pgx.Tx, id uuid.UUID, fields []string, windowMinutes int) error
}

type WebhookService struct {
	pool  *db.Pool
	repo  webhookRepo
	audit *repository.AdminAuditEventRepository
}

func NewWebhookService(pool *db.Pool, repo webhookRepo, audit *repository.AdminAuditEventRepository) *WebhookService {
	return &WebhookService{pool: pool, repo: repo, audit: audit}
}

// webhookAuditFields is the subset of domain.WebhookEndpoint safe to put in
// an admin audit event's data column -- TokenHash never appears; TokenLast4
// is the same masked value already shown in the Settings UI, so it's fine.
func webhookAuditFields(ep *domain.WebhookEndpoint) map[string]any {
	return map[string]any{
		"name": ep.Name, "source": ep.Source, "status": ep.Status, "tokenLast4": ep.TokenLast4,
		"expiresAt": ep.ExpiresAt, "fieldMappingTemplateId": ep.FieldMappingTemplateID,
		"groupByFields": ep.GroupByFields, "dedupWindowMinutes": ep.DedupWindowMinutes,
	}
}

func (s *WebhookService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.WebhookEndpoint, error) {
	var endpoints []domain.WebhookEndpoint
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		endpoints = v
		return err
	})
	return endpoints, err
}

// CreateResult carries the plaintext token exactly once — it is never
// stored (only its hash is) and never retrievable again after this
// response, matching the "Copy URL" masked-token pattern in Settings.
type CreateResult struct {
	Endpoint domain.WebhookEndpoint
	Token    string
}

// Create issues a new webhook endpoint + token. expiresInDays follows the
// same convention as Regenerate (see resolveExpiry): nil defaults to the
// standard 90-day rotation policy, 0/negative means the admin explicitly
// opted this endpoint out of expiring. fieldMappingTemplateID is optional
// and, unlike name/source, can be changed later via SetFieldMappingTemplate
// -- same for groupByFields/dedupWindowMinutes via SetGroupByFields.
// dedupWindowMinutes nil or <= 0 resolves to defaultDedupWindowMinutes
// (see resolveDedupWindow), same shape as resolveExpiry.
func (s *WebhookService) Create(ctx context.Context, tenantID, actorID uuid.UUID, name, source string, expiresInDays *int, fieldMappingTemplateID *uuid.UUID, groupByFields []string, dedupWindowMinutes *int) (*CreateResult, error) {
	token, err := generatePrefixedToken(tokenPrefix, 24)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	ep := &domain.WebhookEndpoint{
		TenantID:               tenantID,
		Name:                   name,
		Source:                 source,
		TokenHash:              hashToken(token),
		TokenLast4:             lastN(token, 4),
		ExpiresAt:              resolveExpiry(expiresInDays),
		CreatedBy:              &actorID,
		FieldMappingTemplateID: fieldMappingTemplateID,
		GroupByFields:          groupByFields,
		DedupWindowMinutes:     resolveDedupWindow(dedupWindowMinutes),
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.Insert(ctx, tx, ep); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": nil, "to": webhookAuditFields(ep)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "webhooks", Action: "create", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("create webhook endpoint: %w", err)
	}

	return &CreateResult{Endpoint: *ep, Token: token}, nil
}

// Regenerate issues a new token for an existing endpoint, invalidating the
// old one immediately — "Regenerate" in Settings -> Webhook Endpoints.
// Rotating also resets the expiry clock (see resolveExpiry).
func (s *WebhookService) Regenerate(ctx context.Context, tenantID, actorID, id uuid.UUID, expiresInDays *int) (string, error) {
	token, err := generatePrefixedToken(tokenPrefix, 24)
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.RotateToken(ctx, tx, id, hashToken(token), lastN(token, 4), resolveExpiry(expiresInDays)); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{"tokenRegenerated": true, "tokenLast4": lastN(token, 4)}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "webhooks", Action: "regenerate-token", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return "", fmt.Errorf("regenerate token: %w", err)
	}
	return token, nil
}

// defaultTokenExpiryDays is the rotation policy applied when the caller
// doesn't say otherwise: a webhook token expires 90 days after it was
// created or last regenerated, so a forgotten integration's credential
// doesn't stay valid forever. An admin can explicitly opt an endpoint out
// by passing 0 (or a negative number), which resolveExpiry treats as
// "never expires" (nil).
const defaultTokenExpiryDays = 90

func resolveExpiry(expiresInDays *int) *time.Time {
	days := defaultTokenExpiryDays
	if expiresInDays != nil {
		if *expiresInDays <= 0 {
			return nil
		}
		days = *expiresInDays
	}
	t := time.Now().AddDate(0, 0, days)
	return &t
}

// defaultDedupWindowMinutes is how long a repeat payload matching an
// endpoint's GroupByFields still counts as the same event, when the admin
// doesn't say otherwise -- see AlertService.Ingest/FindAndIncrementDuplicate.
const defaultDedupWindowMinutes = 30

// resolveDedupWindow mirrors resolveExpiry's shape: nil or <= 0 means "use
// the default", not "0-minute window" (which would never match anything).
// Unlike ExpiresAt there's no "opt out" value here -- GroupByFields being
// empty is what turns dedup off entirely; the window only matters once
// GroupByFields is set.
func resolveDedupWindow(minutes *int) int {
	if minutes == nil || *minutes <= 0 {
		return defaultDedupWindowMinutes
	}
	return *minutes
}

func (s *WebhookService) SetStatus(ctx context.Context, tenantID, actorID, id uuid.UUID, status string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("invalid status %q", status)
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.SetStatus(ctx, tx, id, status); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{"status": status}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "webhooks", Action: "set-status", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// SetFieldMappingTemplate assigns or clears (templateID == nil) which
// field mapping template applies to alerts this endpoint ingests from now
// on -- see Settings -> Webhook Endpoints' "change template" action.
func (s *WebhookService) SetFieldMappingTemplate(ctx context.Context, tenantID, actorID, id uuid.UUID, templateID *uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.SetFieldMappingTemplate(ctx, tx, id, templateID); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{"fieldMappingTemplateId": templateID}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "webhooks", Action: "set-field-mapping-template", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// SetGroupByFields assigns which JSON-path fields (and dedup window) mark
// two payloads on this endpoint as the same event -- see Settings ->
// Webhook Endpoints' group-by editor, same "editable after creation" shape
// as SetFieldMappingTemplate. Passing an empty fields slice turns dedup
// back off. dedupWindowMinutes follows resolveDedupWindow's convention
// (nil/<=0 -> default 30).
func (s *WebhookService) SetGroupByFields(ctx context.Context, tenantID, actorID, id uuid.UUID, fields []string, dedupWindowMinutes *int) error {
	if fields == nil {
		fields = []string{}
	}
	window := resolveDedupWindow(dedupWindowMinutes)
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.SetGroupByFields(ctx, tx, id, fields, window); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{"groupByFields": fields, "dedupWindowMinutes": window}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "webhooks", Action: "set-group-by-fields", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

const tokenPrefix = "whk_"

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
