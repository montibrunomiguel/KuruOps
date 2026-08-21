package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
)

type UserAPITokenRepository struct{}

func NewUserAPITokenRepository() *UserAPITokenRepository {
	return &UserAPITokenRepository{}
}

// ResolveToken finds the token row (and its tenant/user) for a hashed
// personal API token. It deliberately does not go through Pool.WithTenant --
// the tenant isn't known yet, that's what this call determines -- and
// instead opens the narrow, single-purpose RLS carve-out from
// db/migrations/0001_initial_schema.up.sql (the api_token_lookup policy)
// for the duration of one transaction. See
// WebhookRepository.ResolveToken for the identical pattern this mirrors.
func (r *UserAPITokenRepository) ResolveToken(ctx context.Context, pool *db.Pool, tokenHash string) (*domain.UserAPIToken, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, "select set_config('app.api_token_lookup', 'true', true)"); err != nil {
		return nil, fmt.Errorf("set api token lookup context: %w", err)
	}

	var t domain.UserAPIToken
	err = tx.QueryRow(ctx, `
		select id, tenant_id, user_id, expires_at, revoked_at
		from user_api_tokens
		where token_hash = $1`,
		tokenHash,
	).Scan(&t.ID, &t.TenantID, &t.UserID, &t.ExpiresAt, &t.RevokedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve api token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return &t, nil
}

const userAPITokenColumns = `id, tenant_id, user_id, name, token_hash, token_last4, expires_at, created_at, revoked_at`

// Insert creates the token record. The caller has already generated the
// plaintext token, hashed it, and computed the last-4 display value -- this
// method never sees or logs the plaintext.
func (r *UserAPITokenRepository) Insert(ctx context.Context, tx pgx.Tx, t *domain.UserAPIToken) error {
	row := tx.QueryRow(ctx, `
		insert into user_api_tokens (tenant_id, user_id, name, token_hash, token_last4, expires_at)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		t.TenantID, t.UserID, t.Name, t.TokenHash, t.TokenLast4, t.ExpiresAt,
	)
	if err := row.Scan(&t.ID, &t.CreatedAt); err != nil {
		return fmt.Errorf("insert api token: %w", err)
	}
	return nil
}

// ListByUser returns every token (active or revoked) the given user has
// ever created, most recent first -- Profile's "API Tokens" list never
// shows the plaintext again, only name/last-4/expiry/revoked state.
func (r *UserAPITokenRepository) ListByUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]domain.UserAPIToken, error) {
	return queryList(ctx, tx, `select `+userAPITokenColumns+` from user_api_tokens where user_id = $1 order by created_at desc`, scanUserAPIToken, userID)
}

// Revoke sets revoked_at, scoped to (id, userID) so a user can never revoke
// another user's token even if they guess its id -- RLS's tenant_isolation
// already stops a cross-tenant guess, this stops a same-tenant one.
// Revoking an already-revoked token is a no-op, not an error.
func (r *UserAPITokenRepository) Revoke(ctx context.Context, tx pgx.Tx, id, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		update user_api_tokens set revoked_at = now()
		where id = $1 and user_id = $2 and revoked_at is null`,
		id, userID,
	)
	return err
}

func scanUserAPIToken(row pgx.Row) (*domain.UserAPIToken, error) {
	var t domain.UserAPIToken
	err := row.Scan(&t.ID, &t.TenantID, &t.UserID, &t.Name, &t.TokenHash, &t.TokenLast4, &t.ExpiresAt, &t.CreatedAt, &t.RevokedAt)
	if err != nil {
		return nil, fmt.Errorf("scan api token: %w", err)
	}
	return &t, nil
}
