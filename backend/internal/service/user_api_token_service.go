package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// UserAPITokenService backs self-service personal API tokens (Profile ->
// API Tokens) -- a user's own bearer-token alternative to a JWT session.
// Token generation/hashing/expiry reuse the exact same shared helpers
// (generatePrefixedToken, hashToken, lastN, resolveExpiry) WebhookService
// also uses -- same tradeoffs, just a different prefix so the two token
// families are visually distinguishable and middleware.JWTAuth can route
// between them.
type UserAPITokenService struct {
	pool   *db.Pool
	tokens *repository.UserAPITokenRepository
	users  *repository.UserRepository
}

func NewUserAPITokenService(pool *db.Pool, tokens *repository.UserAPITokenRepository, users *repository.UserRepository) *UserAPITokenService {
	return &UserAPITokenService{pool: pool, tokens: tokens, users: users}
}

// CreateAPITokenResult carries the plaintext token exactly once -- it is
// never stored (only its hash is) and never retrievable again after this
// response, same "shown once" contract as WebhookService.CreateResult.
type CreateAPITokenResult struct {
	Token     domain.UserAPIToken
	Plaintext string
}

// Create issues a new personal API token for userID. expiresInDays follows
// resolveExpiry's convention: nil defaults to 90 days, 0/negative means the
// user explicitly opted this token out of expiring.
func (s *UserAPITokenService) Create(ctx context.Context, tenantID, userID uuid.UUID, name string, expiresInDays *int) (*CreateAPITokenResult, error) {
	plaintext, err := generatePrefixedToken(apiTokenPrefix, 24)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	t := &domain.UserAPIToken{
		TenantID:   tenantID,
		UserID:     userID,
		Name:       name,
		TokenHash:  hashToken(plaintext),
		TokenLast4: lastN(plaintext, 4),
		ExpiresAt:  resolveExpiry(expiresInDays),
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.tokens.Insert(ctx, tx, t)
	})
	if err != nil {
		return nil, fmt.Errorf("create api token: %w", err)
	}

	return &CreateAPITokenResult{Token: *t, Plaintext: plaintext}, nil
}

// List returns every token (active or revoked) userID has ever created --
// never the plaintext again, only what Profile's list view needs.
func (s *UserAPITokenService) List(ctx context.Context, tenantID, userID uuid.UUID) ([]domain.UserAPIToken, error) {
	var tokens []domain.UserAPIToken
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.tokens.ListByUser(ctx, tx, userID)
		tokens = v
		return err
	})
	return tokens, err
}

// Revoke invalidates one of userID's own tokens immediately. Scoped to
// (tokenID, userID) at the repository layer -- see
// UserAPITokenRepository.Revoke -- so a user can never revoke a token that
// isn't their own, even within the same tenant.
func (s *UserAPITokenService) Revoke(ctx context.Context, tenantID, userID, tokenID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.tokens.Revoke(ctx, tx, tokenID, userID)
	})
}

// ResolvedIdentity is what a valid personal API token resolves to -- the
// same fields middleware.Claims needs, sourced from the owning user's
// CURRENT Role rather than anything baked into the token itself, so
// deactivating a user or changing their role takes effect on their personal
// tokens immediately, with nothing to keep in sync.
type ResolvedIdentity struct {
	TenantID           uuid.UUID
	UserID             uuid.UUID
	IsAdmin            bool
	ResourceAccess     []string
	AllowedTags        []string
	MustChangePassword bool
}

// Resolve authenticates a bearer token string against user_api_tokens --
// the middleware-facing entry point APITokenAuth calls on every request
// carrying a pat_-prefixed bearer token. Returns (nil, nil), not an error,
// for every "this token doesn't authenticate" case (unknown, revoked,
// expired, or its owning user no longer active) -- same not-found-shaped
// contract as ResolveToken, letting the caller respond 401 uniformly
// without distinguishing why.
func (s *UserAPITokenService) Resolve(ctx context.Context, plaintext string) (*ResolvedIdentity, error) {
	tok, err := s.tokens.ResolveToken(ctx, s.pool, hashToken(plaintext))
	if err != nil {
		return nil, fmt.Errorf("resolve api token: %w", err)
	}
	if tok == nil || tok.RevokedAt != nil {
		return nil, nil
	}
	if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now()) {
		return nil, nil
	}

	var identity *ResolvedIdentity
	err = s.pool.WithTenant(ctx, tok.TenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, tok.UserID)
		if err != nil {
			return err
		}
		if u == nil || !u.IsActive {
			return nil
		}
		identity = &ResolvedIdentity{
			TenantID:           tok.TenantID,
			UserID:             u.ID,
			IsAdmin:            u.Role.IsAdmin,
			ResourceAccess:     []string(u.Role.ResourceAccess),
			AllowedTags:        u.Role.AllowedTags,
			MustChangePassword: u.MustChangePassword,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load api token owner: %w", err)
	}
	return identity, nil
}

const apiTokenPrefix = "pat_"
