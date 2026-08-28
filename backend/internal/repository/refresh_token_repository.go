package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

// RefreshTokenRepository is structurally near-identical to
// PasswordResetRepository -- see that type's doc comment for why this
// duplication (flagged by dupl during the post-hardening-plan audit
// sweep) is deliberately left as-is rather than merged.
type RefreshTokenRepository struct{}

func NewRefreshTokenRepository() *RefreshTokenRepository {
	return &RefreshTokenRepository{}
}

const refreshTokenColumns = `id, tenant_id, user_id, token_hash, expires_at, revoked_at, created_at`

func (r *RefreshTokenRepository) Insert(ctx context.Context, tx pgx.Tx, t *domain.RefreshToken) error {
	row := tx.QueryRow(ctx, `
		insert into refresh_tokens (tenant_id, user_id, token_hash, expires_at)
		values ($1,$2,$3,$4)
		returning id, created_at`,
		t.TenantID, t.UserID, t.TokenHash, t.ExpiresAt,
	)
	if err := row.Scan(&t.ID, &t.CreatedAt); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

// GetByHash returns nil (not an error) when no row matches -- an unknown or
// already-rotated-away hash is an expected input for POST /auth/refresh,
// not a failure.
func (r *RefreshTokenRepository) GetByHash(ctx context.Context, tx pgx.Tx, hash string) (*domain.RefreshToken, error) {
	row := tx.QueryRow(ctx, `select `+refreshTokenColumns+` from refresh_tokens where token_hash = $1`, hash)
	t, err := scanRefreshToken(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

// Revoke marks a single token used/invalid -- called on rotation (the old
// token is revoked the moment its replacement is issued).
func (r *RefreshTokenRepository) Revoke(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `update refresh_tokens set revoked_at = now() where id = $1 and revoked_at is null`, id)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

// RevokeAllForUser invalidates every outstanding refresh token for a user
// -- called on deactivation and the admin "Revoke sessions" action, so a
// live session can be cut off in bounded time (the access token stays
// valid until its own 15-minute expiry, but can no longer be renewed).
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `update refresh_tokens set revoked_at = now() where user_id = $1 and revoked_at is null`, userID)
	if err != nil {
		return fmt.Errorf("revoke all refresh tokens for user: %w", err)
	}
	return nil
}

func scanRefreshToken(row pgx.Row) (*domain.RefreshToken, error) {
	var t domain.RefreshToken
	err := row.Scan(&t.ID, &t.TenantID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
