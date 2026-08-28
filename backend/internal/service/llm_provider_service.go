package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
)

// llmProviderRepo is the subset of *repository.LLMProviderRepository this
// service calls -- an interface, not the concrete type, purely so tests can
// substitute a repo double that fails on demand to exercise the
// error-wrapping branches (a DB call failing mid-transaction) a real
// Postgres integration test can't trigger. *repository.LLMProviderRepository
// already satisfies this implicitly, so every existing constructor call
// site is unaffected -- including the OTHER services (AIAnalysisService)
// that also depend on the concrete repository type directly; that's a
// separate field on a separate struct and is untouched.
type llmProviderRepo interface {
	List(ctx context.Context, tx pgx.Tx) ([]domain.LLMProvider, error)
	Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.LLMProvider, error)
	Insert(ctx context.Context, tx pgx.Tx, p *domain.LLMProvider) error
	Update(ctx context.Context, tx pgx.Tx, p *domain.LLMProvider) error
	SetDefault(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) error
	Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
}

type LLMProviderService struct {
	pool    *db.Pool
	repo    llmProviderRepo
	secrets secrets.Store
	audit   *repository.AdminAuditEventRepository
}

func NewLLMProviderService(pool *db.Pool, repo llmProviderRepo, store secrets.Store, audit *repository.AdminAuditEventRepository) *LLMProviderService {
	return &LLMProviderService{pool: pool, repo: repo, secrets: store, audit: audit}
}

// llmProviderAuditFields is the subset of domain.LLMProvider safe to put in
// an admin audit event's data column -- APIKeySecretRef is an opaque
// reference into secrets.Store, not the plaintext key, so it's fine to
// include, but is deliberately left out anyway since it's meaningless to a
// human reading the audit log and only ever changes as a side effect of
// AutoAnalyzeAllAlerts/other real field edits, never a fact worth diffing
// on its own.
func llmProviderAuditFields(p *domain.LLMProvider) map[string]any {
	return map[string]any{
		"name": p.Name, "kind": p.Kind, "baseURL": p.BaseURL, "model": p.Model,
		"autoAnalyzeAllAlerts": p.AutoAnalyzeAllAlerts,
	}
}

func (s *LLMProviderService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.LLMProvider, error) {
	var providers []domain.LLMProvider
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		providers = v
		return err
	})
	return providers, err
}

// SaveInput carries the plaintext API key from the Settings -> AI
// Integration form. It never reaches the repository or the database —
// Create/Update resolve it to a secrets.Store reference first.
type LLMProviderSaveInput struct {
	Name    string
	Kind    string
	BaseURL *string
	Model   string
	APIKey  string
	// AutoAnalyzeAllAlerts -- see domain.LLMProvider's doc comment.
	AutoAnalyzeAllAlerts bool
}

// Create validates that a custom endpoint is only allowed for the kinds
// that support one, stores the API key via secrets.Store, and persists only
// the resulting reference — see the architecture review, "Camada de LLM
// plugável", for why "openai_compatible" + BaseURL is the general case
// instead of one adapter per vendor.
func (s *LLMProviderService) Create(ctx context.Context, tenantID, actorID uuid.UUID, in LLMProviderSaveInput) (*domain.LLMProvider, error) {
	if err := validateLLMKind(in.Kind, in.BaseURL); err != nil {
		return nil, err
	}

	ref, err := s.secrets.Put(ctx, tenantID.String(), "llm:"+in.Name, in.APIKey)
	if err != nil {
		return nil, fmt.Errorf("store api key: %w", err)
	}

	p := &domain.LLMProvider{
		TenantID:             tenantID,
		Name:                 in.Name,
		Kind:                 in.Kind,
		BaseURL:              in.BaseURL,
		Model:                in.Model,
		APIKeySecretRef:      ref,
		AutoAnalyzeAllAlerts: in.AutoAnalyzeAllAlerts,
		CreatedBy:            &actorID,
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.Insert(ctx, tx, p); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": nil, "to": llmProviderAuditFields(p)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "ai-integration", Action: "create", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("create llm provider: %w", err)
	}
	return p, nil
}

// Update changes name/kind/base_url/model, and rotates the API key only if
// a non-empty one is supplied (an empty APIKey means "keep the existing
// key" — the Settings form never round-trips the real key back to the
// client to prefill it).
func (s *LLMProviderService) Update(ctx context.Context, tenantID, actorID, id uuid.UUID, in LLMProviderSaveInput) (*domain.LLMProvider, error) {
	if err := validateLLMKind(in.Kind, in.BaseURL); err != nil {
		return nil, err
	}

	var updated *domain.LLMProvider
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.Get(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("load llm provider: %w", err)
		}
		if existing == nil {
			return fmt.Errorf("llm provider %s not found", id)
		}
		before := llmProviderAuditFields(existing)
		before["apiKeyRotated"] = false

		ref := existing.APIKeySecretRef
		if in.APIKey != "" {
			ref, err = s.secrets.Put(ctx, tenantID.String(), "llm:"+in.Name, in.APIKey)
			if err != nil {
				return fmt.Errorf("store api key: %w", err)
			}
		}

		existing.Name = in.Name
		existing.Kind = in.Kind
		existing.BaseURL = in.BaseURL
		existing.Model = in.Model
		existing.APIKeySecretRef = ref
		existing.AutoAnalyzeAllAlerts = in.AutoAnalyzeAllAlerts

		if err := s.repo.Update(ctx, tx, existing); err != nil {
			return fmt.Errorf("update llm provider: %w", err)
		}
		updated = existing

		after := llmProviderAuditFields(existing)
		after["apiKeyRotated"] = in.APIKey != ""
		data, _ := json.Marshal(map[string]any{"from": before, "to": after})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "ai-integration", Action: "update", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *LLMProviderService) SetDefault(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.SetDefault(ctx, tx, tenantID, id); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{"defaultProviderId": id}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "ai-integration", Action: "set-default", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

func (s *LLMProviderService) Delete(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.repo.Delete(ctx, tx, id); err != nil {
			return err
		}
		var from any
		if before != nil {
			from = llmProviderAuditFields(before)
		}
		data, _ := json.Marshal(map[string]any{"from": from, "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "ai-integration", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// openAICompatibleKinds are the provider kinds that accept a custom
// BaseURL — self-hosted/enterprise endpoints. Fixed-endpoint kinds
// (anthropic) reject one so an admin can't accidentally point a "vanilla"
// provider entry at an unrelated host.
func validateLLMKind(kind string, baseURL *string) error {
	switch kind {
	case "anthropic":
		if baseURL != nil && *baseURL != "" {
			return fmt.Errorf("kind %q does not accept a custom base_url; use openai_compatible or self_hosted for custom endpoints", kind)
		}
	case "openai_compatible", "azure_openai", "self_hosted":
		// base_url required in practice for these, left to UI validation
	default:
		return fmt.Errorf("unknown llm provider kind %q", kind)
	}
	return nil
}
