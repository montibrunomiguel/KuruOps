package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type PersonalAccessTokenRepository struct{}

func NewPersonalAccessTokenRepository() *PersonalAccessTokenRepository {
	return &PersonalAccessTokenRepository{}
}

const patColumns = `id, tenant_id, user_id, name, token_hash, token_last4, expires_at, last_used_at, revoked_at, created_at`

func (r *PersonalAccessTokenRepository) Insert(ctx context.Context, tx pgx.Tx, t *domain.PersonalAccessToken) error {
	row := tx.QueryRow(ctx, `
		insert into personal_access_tokens (tenant_id, user_id, name, token_hash, token_last4, expires_at)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		t.TenantID, t.UserID, t.Name, t.TokenHash, t.TokenLast4, t.ExpiresAt,
	)
	if err := row.Scan(&t.ID, &t.CreatedAt); err != nil {
		return fmt.Errorf("insert personal access token: %w", err)
	}
	return nil
}

// ListForUser includes revoked tokens (the UI shows them struck through,
// see Settings -> My Account -- API Tokens) so a user can see what they
// revoked and when, not just what's still live.
func (r *PersonalAccessTokenRepository) ListForUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]domain.PersonalAccessToken, error) {
	rows, err := tx.Query(ctx, `select `+patColumns+` from personal_access_tokens where user_id = $1 order by created_at desc`, userID)
	if err != nil {
		return nil, fmt.Errorf("list personal access tokens: %w", err)
	}
	defer rows.Close()

	var out []domain.PersonalAccessToken
	for rows.Next() {
		t, err := scanPAT(rows)
		if err != nil {
			return nil, fmt.Errorf("scan personal access token: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetByHash returns nil (not an error) when no row matches -- an unknown,
// revoked-and-forgotten, or never-issued hash is an expected input on the
// auth path (see middleware's PAT resolver), not a failure.
func (r *PersonalAccessTokenRepository) GetByHash(ctx context.Context, tx pgx.Tx, hash string) (*domain.PersonalAccessToken, error) {
	row := tx.QueryRow(ctx, `select `+patColumns+` from personal_access_tokens where token_hash = $1`, hash)
	t, err := scanPAT(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get personal access token by hash: %w", err)
	}
	return t, nil
}

func (r *PersonalAccessTokenRepository) StampLastUsed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `update personal_access_tokens set last_used_at = now() where id = $1`, id)
	if err != nil {
		return fmt.Errorf("stamp personal access token last used: %w", err)
	}
	return nil
}

// Revoke is scoped to (id, user_id) -- called from the owning user's own
// Settings -> My Account, never cross-user, so the WHERE clause itself is
// the authorization check, same principle as every other "revoke my own
// X" mutation in this codebase.
func (r *PersonalAccessTokenRepository) Revoke(ctx context.Context, tx pgx.Tx, id, userID uuid.UUID) error {
	tag, err := tx.Exec(ctx, `update personal_access_tokens set revoked_at = now() where id = $1 and user_id = $2 and revoked_at is null`, id, userID)
	if err != nil {
		return fmt.Errorf("revoke personal access token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("personal access token not found")
	}
	return nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanPAT(row scannable) (*domain.PersonalAccessToken, error) {
	var t domain.PersonalAccessToken
	err := row.Scan(&t.ID, &t.TenantID, &t.UserID, &t.Name, &t.TokenHash, &t.TokenLast4, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
