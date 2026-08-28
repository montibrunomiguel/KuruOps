package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

type LLMProviderRepository struct{}

func NewLLMProviderRepository() *LLMProviderRepository {
	return &LLMProviderRepository{}
}

const llmProviderColumns = `
	id, tenant_id, name, kind, base_url, model, api_key_secret_ref, is_default, auto_analyze_all_alerts, created_by, created_at, updated_at`

func (r *LLMProviderRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.LLMProvider, error) {
	return queryList(ctx, tx, `select `+llmProviderColumns+` from llm_providers order by created_at asc`, scanLLMProvider)
}

func (r *LLMProviderRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.LLMProvider, error) {
	return queryOne(ctx, tx, `select `+llmProviderColumns+` from llm_providers where id = $1`, scanLLMProvider, id)
}

// GetDefault returns the tenant's default provider (see SetDefault -- the
// unique partial index guarantees at most one), or nil if none has been
// configured yet.
func (r *LLMProviderRepository) GetDefault(ctx context.Context, tx pgx.Tx) (*domain.LLMProvider, error) {
	return queryOne(ctx, tx, `select `+llmProviderColumns+` from llm_providers where is_default = true limit 1`, scanLLMProvider)
}

func (r *LLMProviderRepository) Insert(ctx context.Context, tx pgx.Tx, p *domain.LLMProvider) error {
	row := tx.QueryRow(ctx, `
		insert into llm_providers (tenant_id, name, kind, base_url, model, api_key_secret_ref, is_default, auto_analyze_all_alerts, created_by)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		returning id, created_at, updated_at`,
		p.TenantID, p.Name, p.Kind, p.BaseURL, p.Model, p.APIKeySecretRef, p.IsDefault, p.AutoAnalyzeAllAlerts, p.CreatedBy,
	)
	if err := row.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return fmt.Errorf("insert llm provider: %w", err)
	}
	return nil
}

func (r *LLMProviderRepository) Update(ctx context.Context, tx pgx.Tx, p *domain.LLMProvider) error {
	_, err := tx.Exec(ctx, `
		update llm_providers
		set name = $2, kind = $3, base_url = $4, model = $5, api_key_secret_ref = $6, auto_analyze_all_alerts = $7, updated_at = now()
		where id = $1`,
		p.ID, p.Name, p.Kind, p.BaseURL, p.Model, p.APIKeySecretRef, p.AutoAnalyzeAllAlerts,
	)
	if err != nil {
		return fmt.Errorf("update llm provider: %w", err)
	}
	return nil
}

// SetDefault clears is_default on every other provider for the tenant and
// sets it on id, atomically — the unique partial index
// llm_providers_one_default_per_tenant only allows one row with
// is_default = true, so clearing first avoids a constraint violation.
func (r *LLMProviderRepository) SetDefault(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `update llm_providers set is_default = false where tenant_id = $1`, tenantID); err != nil {
		return fmt.Errorf("clear default llm provider: %w", err)
	}
	_, err := tx.Exec(ctx, `update llm_providers set is_default = true where id = $1`, id)
	if err != nil {
		return fmt.Errorf("set default llm provider: %w", err)
	}
	return nil
}

func (r *LLMProviderRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from llm_providers where id = $1`, id)
	return err
}

func scanLLMProvider(row pgx.Row) (*domain.LLMProvider, error) {
	var p domain.LLMProvider
	err := row.Scan(&p.ID, &p.TenantID, &p.Name, &p.Kind, &p.BaseURL, &p.Model, &p.APIKeySecretRef, &p.IsDefault, &p.AutoAnalyzeAllAlerts, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan llm provider: %w", err)
	}
	return &p, nil
}
