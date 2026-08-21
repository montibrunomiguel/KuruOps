package repository

import (
	"context"
	"errors"
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
// db/migrations/0001_initial_schema.up.sql (the webhook_token_lookup
// policy) for the duration of one transaction.
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
		select id, tenant_id, source, status, expires_at, field_mapping_template_id,
		       group_by_fields, dedup_window_minutes
		from webhook_endpoints
		where token_hash = $1`,
		tokenHash,
	).Scan(
		&ep.ID, &ep.TenantID, &ep.Source, &ep.Status, &ep.ExpiresAt, &ep.FieldMappingTemplateID,
		&ep.GroupByFields, &ep.DedupWindowMinutes,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve webhook token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return &ep, nil
}

const webhookColumns = `id, tenant_id, name, source, token_hash, token_last4, status, rotated_at, expires_at, created_by, created_at, field_mapping_template_id, group_by_fields, dedup_window_minutes`

func (r *WebhookRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.WebhookEndpoint, error) {
	return queryList(ctx, tx, `select `+webhookColumns+` from webhook_endpoints order by created_at desc`, scanWebhookEndpoint)
}

func (r *WebhookRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.WebhookEndpoint, error) {
	return queryOne(ctx, tx, `select `+webhookColumns+` from webhook_endpoints where id = $1`, scanWebhookEndpoint, id)
}

// Insert creates the endpoint record. The caller has already generated the
// plaintext token, hashed it, and computed the last-4 display value — this
// method never sees or logs the plaintext. FieldMappingTemplateID is
// optional and, unlike the rest of these fields, changeable afterward — see
// SetFieldMappingTemplate.
func (r *WebhookRepository) Insert(ctx context.Context, tx pgx.Tx, ep *domain.WebhookEndpoint) error {
	// group_by_fields is NOT NULL -- defaulted here (not just in
	// WebhookService.Create) so every direct-repository caller (every test
	// fixture built before this column existed) keeps working without
	// having to set GroupByFields itself, same reasoning as
	// AlertRepository.Insert's Metadata default: a Go nil slice encodes as
	// SQL NULL, not '{}', so this can't be left to the column's own
	// `default '{}'` -- that default only fires when the column is omitted
	// from the INSERT entirely, and it's always named explicitly here.
	if ep.GroupByFields == nil {
		ep.GroupByFields = []string{}
	}
	row := tx.QueryRow(ctx, `
		insert into webhook_endpoints (
			tenant_id, name, source, token_hash, token_last4, expires_at, created_by,
			field_mapping_template_id, group_by_fields, dedup_window_minutes
		)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		returning id, status, created_at`,
		ep.TenantID, ep.Name, ep.Source, ep.TokenHash, ep.TokenLast4, ep.ExpiresAt, ep.CreatedBy,
		ep.FieldMappingTemplateID, ep.GroupByFields, ep.DedupWindowMinutes,
	)
	if err := row.Scan(&ep.ID, &ep.Status, &ep.CreatedAt); err != nil {
		return fmt.Errorf("insert webhook endpoint: %w", err)
	}
	return nil
}

// SetGroupByFields assigns the JSON-path list (and dedup window) that
// determine which incoming payloads on this endpoint are treated as
// duplicates of an existing alert -- see Settings -> Webhook Endpoints'
// "group by fields" editor, same "editable after creation" shape as
// SetFieldMappingTemplate. Passing an empty fields slice turns dedup back
// off for this endpoint (a fresh alert every time, today's behavior).
func (r *WebhookRepository) SetGroupByFields(ctx context.Context, tx pgx.Tx, id uuid.UUID, fields []string, windowMinutes int) error {
	_, err := tx.Exec(ctx,
		`update webhook_endpoints set group_by_fields = $2, dedup_window_minutes = $3 where id = $1`,
		id, fields, windowMinutes,
	)
	return err
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
		&ep.GroupByFields, &ep.DedupWindowMinutes,
	)
	if err != nil {
		return nil, fmt.Errorf("scan webhook endpoint: %w", err)
	}
	return &ep, nil
}
