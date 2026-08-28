package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

// MFAPendingTokenRepository backs the single-use token a local login for a
// TOTP-enrolled user is issued between "password verified" and "code
// verified" -- see service.AuthService.LoginLocal/VerifyMFA and
// db/migrations/0009_mfa_pending_tokens.up.sql.
type MFAPendingTokenRepository struct{}

func NewMFAPendingTokenRepository() *MFAPendingTokenRepository {
	return &MFAPendingTokenRepository{}
}

func (r *MFAPendingTokenRepository) Insert(ctx context.Context, tx pgx.Tx, t *domain.MFAPendingToken) error {
	row := tx.QueryRow(ctx, `
		insert into mfa_pending_tokens (tenant_id, user_id, token_hash, expires_at)
		values ($1,$2,$3,$4)
		returning id, created_at`,
		t.TenantID, t.UserID, t.TokenHash, t.ExpiresAt,
	)
	if err := row.Scan(&t.ID, &t.CreatedAt); err != nil {
		return fmt.Errorf("insert mfa pending token: %w", err)
	}
	return nil
}

// GetByHash returns the matching, not-yet-consumed, not-yet-expired row --
// deliberately non-mutating, unlike OAuthStateRepository's atomic
// ConsumeByHash: an OAuth callback is inherently single-shot, but a wrong
// TOTP code is a normal, expected mistake a caller should get to retry
// (bounded by AuthHandlers' per-pendingToken rate limit, and by
// mfaPendingTokenTTL itself) without having to restart the whole login from
// the password step. Returns nil, nil (not an error) when nothing matches
// -- an unknown, already-consumed, or expired token is an expected input
// for a verify endpoint, not a failure worth distinguishing from "wrong
// code" (a login form must not be able to enumerate which failure mode
// occurred).
func (r *MFAPendingTokenRepository) GetByHash(ctx context.Context, tx pgx.Tx, hash string) (*domain.MFAPendingToken, error) {
	row := tx.QueryRow(ctx, `
		select id, tenant_id, user_id, token_hash, expires_at, consumed_at, created_at
		from mfa_pending_tokens
		where token_hash = $1 and consumed_at is null and expires_at > now()`,
		hash,
	)
	t, err := scanMFAPendingToken(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get mfa pending token: %w", err)
	}
	return t, nil
}

// MarkConsumed is called only once VerifyMFA has confirmed the TOTP code
// was correct -- see GetByHash's doc comment for why consumption is
// deferred to success rather than happening on every attempt.
func (r *MFAPendingTokenRepository) MarkConsumed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `update mfa_pending_tokens set consumed_at = now() where id = $1 and consumed_at is null`, id)
	if err != nil {
		return fmt.Errorf("mark mfa pending token consumed: %w", err)
	}
	return nil
}

func scanMFAPendingToken(row pgx.Row) (*domain.MFAPendingToken, error) {
	var t domain.MFAPendingToken
	if err := row.Scan(&t.ID, &t.TenantID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.ConsumedAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}
