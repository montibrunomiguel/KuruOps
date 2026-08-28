package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

// PasswordResetRepository is structurally near-identical to
// RefreshTokenRepository (same hashed/expiring/single-use-token shape,
// same Insert/GetByHash/mark-consumed/delete-all methods) -- flagged by
// dupl during the post-hardening-plan audit sweep and deliberately left
// unmerged rather than extracted into a shared generic repository: both
// are small, stable, security-critical auth-token stores that are fully
// tested and working today, and a generic-over-domain-type extraction
// here would touch both without adding real safety margin. Worth
// revisiting only if a third token table with this same shape shows up.
type PasswordResetRepository struct{}

func NewPasswordResetRepository() *PasswordResetRepository {
	return &PasswordResetRepository{}
}

const passwordResetTokenColumns = `id, tenant_id, user_id, token_hash, expires_at, used_at, created_at`

func (r *PasswordResetRepository) Insert(ctx context.Context, tx pgx.Tx, t *domain.PasswordResetToken) error {
	row := tx.QueryRow(ctx, `
		insert into password_reset_tokens (tenant_id, user_id, token_hash, expires_at)
		values ($1,$2,$3,$4)
		returning id, created_at`,
		t.TenantID, t.UserID, t.TokenHash, t.ExpiresAt,
	)
	if err := row.Scan(&t.ID, &t.CreatedAt); err != nil {
		return fmt.Errorf("insert password reset token: %w", err)
	}
	return nil
}

// GetByHash returns nil (not an error) when no row matches -- an unknown or
// already-used hash is an expected input for the confirm step, not a
// failure.
func (r *PasswordResetRepository) GetByHash(ctx context.Context, tx pgx.Tx, hash string) (*domain.PasswordResetToken, error) {
	row := tx.QueryRow(ctx, `select `+passwordResetTokenColumns+` from password_reset_tokens where token_hash = $1`, hash)
	t, err := scanPasswordResetToken(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return t, nil
}

func (r *PasswordResetRepository) MarkUsed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `update password_reset_tokens set used_at = now() where id = $1 and used_at is null`, id)
	if err != nil {
		return fmt.Errorf("mark password reset token used: %w", err)
	}
	return nil
}

// DeleteAllForUser invalidates every outstanding reset token for a user --
// called after a successful reset, so an older still-unexpired link can't
// also be used (the "successful auth action invalidates other pending
// ones" principle also applied by RefreshToken rotation).
func (r *PasswordResetRepository) DeleteAllForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from password_reset_tokens where user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete password reset tokens for user: %w", err)
	}
	return nil
}

func scanPasswordResetToken(row pgx.Row) (*domain.PasswordResetToken, error) {
	var t domain.PasswordResetToken
	err := row.Scan(&t.ID, &t.TenantID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.UsedAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
