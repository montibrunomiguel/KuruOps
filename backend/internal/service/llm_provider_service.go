package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

type LLMProviderService struct {
	pool    *db.Pool
	repo    *repository.LLMProviderRepository
	secrets secrets.Store
}

func NewLLMProviderService(pool *db.Pool, repo *repository.LLMProviderRepository, store secrets.Store) *LLMProviderService {
	return &LLMProviderService{pool: pool, repo: repo, secrets: store}
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
		TenantID:        tenantID,
		Name:            in.Name,
		Kind:            in.Kind,
		BaseURL:         in.BaseURL,
		Model:           in.Model,
		APIKeySecretRef: ref,
		CreatedBy:       &actorID,
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, p)
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
func (s *LLMProviderService) Update(ctx context.Context, tenantID, id uuid.UUID, in LLMProviderSaveInput) (*domain.LLMProvider, error) {
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

		if err := s.repo.Update(ctx, tx, existing); err != nil {
			return fmt.Errorf("update llm provider: %w", err)
		}
		updated = existing
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *LLMProviderService) SetDefault(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.SetDefault(ctx, tx, tenantID, id)
	})
}

func (s *LLMProviderService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
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
