package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
)

type WebhookRepository struct{}

func NewWebhookRepository() *WebhookRepository {
	return &WebhookRepository{}
}

// ResolveToken finds the endpoint (and its tenant) for a hashed webhook
// token. It deliberately does not go through Pool.WithTenant — the tenant
// isn't known yet, that's what this call determines — and instead opens the
// narrow, single-purpose RLS carve-out from
// db/migrations/0010_webhook_token_lookup_policy.up.sql for the duration of
// one transaction. See that migration for why this is safe.
func (r *WebhookRepository) ResolveToken(ctx context.Context, pool *db.Pool, tokenHash string) (*domain.WebhookEndpoint, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, "select set_config('app.webhook_lookup', 'true', true)"); err != nil {
		return nil, fmt.Errorf("set webhook lookup context: %w", err)
	}

	var ep domain.WebhookEndpoint
	err = tx.QueryRow(ctx, `
		select id, tenant_id, source, status, expires_at, field_mapping_template_id
		from webhook_endpoints
		where token_hash = $1`,
		tokenHash,
	).Scan(&ep.ID, &ep.TenantID, &ep.Source, &ep.Status, &ep.ExpiresAt, &ep.FieldMappingTemplateID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve webhook token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return &ep, nil
}

const webhookColumns = `id, tenant_id, name, source, token_hash, token_last4, status, rotated_at, expires_at, created_by, created_at, field_mapping_template_id`

func (r *WebhookRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.WebhookEndpoint, error) {
	rows, err := tx.Query(ctx, `select `+webhookColumns+` from webhook_endpoints order by created_at desc`)
	if err != nil {
		return nil, fmt.Errorf("query webhook endpoints: %w", err)
	}
	defer rows.Close()

	endpoints := []domain.WebhookEndpoint{}
	for rows.Next() {
		ep, err := scanWebhookEndpoint(rows)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, *ep)
	}
	return endpoints, rows.Err()
}

func (r *WebhookRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.WebhookEndpoint, error) {
	row := tx.QueryRow(ctx, `select `+webhookColumns+` from webhook_endpoints where id = $1`, id)
	return scanWebhookEndpoint(row)
}

// Insert creates the endpoint record. The caller has already generated the
// plaintext token, hashed it, and computed the last-4 display value — this
// method never sees or logs the plaintext. FieldMappingTemplateID is
// optional and, unlike the rest of these fields, changeable afterward — see
// SetFieldMappingTemplate.
func (r *WebhookRepository) Insert(ctx context.Context, tx pgx.Tx, ep *domain.WebhookEndpoint) error {
	row := tx.QueryRow(ctx, `
		insert into webhook_endpoints (tenant_id, name, source, token_hash, token_last4, expires_at, created_by, field_mapping_template_id)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		returning id, status, created_at`,
		ep.TenantID, ep.Name, ep.Source, ep.TokenHash, ep.TokenLast4, ep.ExpiresAt, ep.CreatedBy, ep.FieldMappingTemplateID,
	)
	if err := row.Scan(&ep.ID, &ep.Status, &ep.CreatedAt); err != nil {
		return fmt.Errorf("insert webhook endpoint: %w", err)
	}
	return nil
}

// SetFieldMappingTemplate assigns or clears (templateID == nil) the field
// mapping template applied to every alert this endpoint ingests from now
// on -- see Settings -> Webhook Endpoints' "change template" action, the
// one part of an endpoint's config that stays editable after creation.
func (r *WebhookRepository) SetFieldMappingTemplate(ctx context.Context, tx pgx.Tx, id uuid.UUID, templateID *uuid.UUID) error {
	_, err := tx.Exec(ctx, `update webhook_endpoints set field_mapping_template_id = $2 where id = $1`, id, templateID)
	return err
}

// RotateToken replaces the token hash/last-4 for an existing endpoint —
// "Regenerate" in Settings. The old token stops authenticating immediately.
// expiresAt is reset alongside the token, matching a "rotation resets the
// clock" policy (nil = admin opted this endpoint out of expiring).
func (r *WebhookRepository) RotateToken(ctx context.Context, tx pgx.Tx, id uuid.UUID, tokenHash, tokenLast4 string, expiresAt *time.Time) error {
	_, err := tx.Exec(ctx, `
		update webhook_endpoints set token_hash = $2, token_last4 = $3, rotated_at = now(), expires_at = $4
		where id = $1`,
		id, tokenHash, tokenLast4, expiresAt,
	)
	return err
}

func (r *WebhookRepository) SetStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status string) error {
	_, err := tx.Exec(ctx, `update webhook_endpoints set status = $2 where id = $1`, id, status)
	return err
}

func scanWebhookEndpoint(row pgx.Row) (*domain.WebhookEndpoint, error) {
	var ep domain.WebhookEndpoint
	err := row.Scan(
		&ep.ID, &ep.TenantID, &ep.Name, &ep.Source, &ep.TokenHash, &ep.TokenLast4, &ep.Status, &ep.RotatedAt,
		&ep.ExpiresAt, &ep.CreatedBy, &ep.CreatedAt, &ep.FieldMappingTemplateID,
	)
	if err != nil {
		return nil, fmt.Errorf("scan webhook endpoint: %w", err)
	}
	return &ep, nil
}
