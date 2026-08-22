package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

// OAuthStateRepository backs the single-use CSRF state every "Connect to
// <provider>" OAuth flow this app initiates goes through -- see
// service.OAuthStateService and db/migrations/0003_oauth_states.up.sql.
type OAuthStateRepository struct{}

func NewOAuthStateRepository() *OAuthStateRepository {
	return &OAuthStateRepository{}
}

func (r *OAuthStateRepository) Insert(ctx context.Context, tx pgx.Tx, s *domain.OAuthState) error {
	metadata, err := json.Marshal(s.Metadata)
	if err != nil {
		return fmt.Errorf("marshal oauth state metadata: %w", err)
	}
	row := tx.QueryRow(ctx, `
		insert into oauth_states (tenant_id, user_id, provider, token_hash, metadata, expires_at)
		values ($1,$2,$3,$4,$5,$6)
		returning id, created_at`,
		s.TenantID, s.UserID, s.Provider, s.TokenHash, metadata, s.ExpiresAt,
	)
	if err := row.Scan(&s.ID, &s.CreatedAt); err != nil {
		return fmt.Errorf("insert oauth state: %w", err)
	}
	return nil
}

// ConsumeByHash atomically marks the matching, not-yet-consumed,
// not-yet-expired row consumed and returns it in one statement -- unlike
// PasswordResetRepository's GetByHash-then-MarkUsed pair, this closes the
// TOCTOU race outright rather than leaving it open (see the same OAuth
// callback being hit twice concurrently, e.g. a doubled browser request or
// a replayed callback URL). Returns nil, nil (not an error) when nothing
// matches -- an unknown, already-consumed, or expired state is an expected
// input for a callback handler, not a failure.
func (r *OAuthStateRepository) ConsumeByHash(ctx context.Context, tx pgx.Tx, provider domain.OAuthProvider, hash string) (*domain.OAuthState, error) {
	row := tx.QueryRow(ctx, `
		update oauth_states set consumed_at = now()
		where token_hash = $1 and provider = $2 and consumed_at is null and expires_at > now()
		returning id, tenant_id, user_id, provider, token_hash, metadata, expires_at, consumed_at, created_at`,
		hash, provider,
	)
	s, err := scanOAuthState(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("consume oauth state: %w", err)
	}
	return s, nil
}

func scanOAuthState(row pgx.Row) (*domain.OAuthState, error) {
	var s domain.OAuthState
	var metadata []byte
	if err := row.Scan(&s.ID, &s.TenantID, &s.UserID, &s.Provider, &s.TokenHash, &metadata, &s.ExpiresAt, &s.ConsumedAt, &s.CreatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(metadata, &s.Metadata); err != nil {
		return nil, fmt.Errorf("unmarshal oauth state metadata: %w", err)
	}
	return &s, nil
}
