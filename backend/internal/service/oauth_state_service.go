package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
)

// oauthStateTTL bounds how long an admin has to complete a "Connect to
// <provider>" OAuth round trip (click Connect -> provider's consent
// screen -> callback) before the state token expires and the callback
// rejects it -- generous enough for a human to actually read the consent
// screen, short enough that a leaked/logged authorize URL isn't usable for
// long.
const oauthStateTTL = 10 * time.Minute

const oauthStateTokenPrefix = "oauthst_"

// OAuthStateService is the CSRF-protection layer shared by every 3-legged
// OAuth flow this app initiates (Google Drive's connect flow today, Slack's
// in a later PR) -- see OAuthState's doc comment for why this is a shared
// DB-backed token rather than a per-provider stateless signature. Nothing
// about the token exchange itself (each provider's authorize-URL shape,
// token-response fields) is shared -- only this generate/consume step is,
// mirroring tokens.go's own precedent for consolidating identical
// duplicated logic once, not before.
type OAuthStateService struct {
	pool *db.Pool
	repo *repository.OAuthStateRepository
}

func NewOAuthStateService(pool *db.Pool, repo *repository.OAuthStateRepository) *OAuthStateService {
	return &OAuthStateService{pool: pool, repo: repo}
}

// Generate issues a fresh, single-use state token for tenantID/userID
// starting a provider's OAuth flow, persists its hash, and returns the
// plaintext to embed in the authorize URL's `state` param. metadata
// carries whatever small bit of context needs to survive the redirect
// round trip (e.g. Google Drive's chosen target folder ID) -- nil is fine
// when a provider doesn't need any.
func (s *OAuthStateService) Generate(ctx context.Context, tenantID, userID uuid.UUID, provider domain.OAuthProvider, metadata map[string]string) (string, error) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	plaintext, err := generatePrefixedToken(oauthStateTokenPrefix, 32)
	if err != nil {
		return "", fmt.Errorf("generate oauth state token: %w", err)
	}
	state := &domain.OAuthState{
		TenantID:  tenantID,
		UserID:    userID,
		Provider:  provider,
		TokenHash: hashToken(plaintext),
		Metadata:  metadata,
		ExpiresAt: time.Now().Add(oauthStateTTL),
	}
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, state)
	})
	if err != nil {
		return "", fmt.Errorf("persist oauth state: %w", err)
	}
	return plaintext, nil
}

// Consume validates and single-uses the state token a provider's callback
// echoed back, returning the row (with its Metadata and UserID -- the
// installing admin, since the callback request itself carries no session
// of its own) on success. Returns nil, nil (not an error) for an unknown,
// already-consumed, or expired state -- callers should treat that as "this
// callback isn't valid," not a server failure.
func (s *OAuthStateService) Consume(ctx context.Context, tenantID uuid.UUID, provider domain.OAuthProvider, state string) (*domain.OAuthState, error) {
	var consumed *domain.OAuthState
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.ConsumeByHash(ctx, tx, provider, hashToken(state))
		consumed = v
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("consume oauth state: %w", err)
	}
	return consumed, nil
}
