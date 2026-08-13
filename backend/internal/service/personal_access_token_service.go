package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// PersonalAccessTokenService backs Settings -> My Account -- API Tokens:
// lets a user mint/revoke their own bearer credentials for scripted access
// to the API, and (via Resolve) is what authenticates a request that
// presents one. See internal/httpserver/middleware.PATResolver, the
// interface this satisfies structurally without importing that package
// (keeps this layer transport-agnostic).
type PersonalAccessTokenService struct {
	pool    *db.Pool
	tenants *repository.TenantRepository
	users   *repository.UserRepository
	tokens  *repository.PersonalAccessTokenRepository
}

func NewPersonalAccessTokenService(pool *db.Pool, tenants *repository.TenantRepository, users *repository.UserRepository, tokens *repository.PersonalAccessTokenRepository) *PersonalAccessTokenService {
	return &PersonalAccessTokenService{pool: pool, tenants: tenants, users: users, tokens: tokens}
}

const patPrefix = "pat_"

func generatePAT() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return patPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashPAT(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreatePATResult carries the plaintext token exactly once -- it is never
// stored (only its hash is) and never retrievable again after this
// response, same discipline as WebhookService.CreateResult.
type CreatePATResult struct {
	Token     domain.PersonalAccessToken
	Plaintext string
}

// Create mints a new token for userID, scoped to whatever tenantID/userID
// the caller (the authenticated request's own identity, never a
// client-supplied user id -- see AccountHandlers) resolves to. A nil or
// non-positive expiresInDays means "never expires" -- unlike a webhook
// token (WebhookService.resolveExpiry defaults to 90 days), a script or
// integration silently losing its credential on an unannounced schedule is
// a worse failure mode here than for a webhook endpoint an admin actively
// manages, so this defaults to the opposite of that policy.
func (s *PersonalAccessTokenService) Create(ctx context.Context, tenantID, userID uuid.UUID, name string, expiresInDays *int) (*CreatePATResult, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	plaintext, err := generatePAT()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	pat := &domain.PersonalAccessToken{
		TenantID:   tenantID,
		UserID:     userID,
		Name:       name,
		TokenHash:  hashPAT(plaintext),
		TokenLast4: lastN(plaintext, 4),
		ExpiresAt:  resolvePATExpiry(expiresInDays),
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.tokens.Insert(ctx, tx, pat)
	})
	if err != nil {
		return nil, fmt.Errorf("create personal access token: %w", err)
	}
	return &CreatePATResult{Token: *pat, Plaintext: plaintext}, nil
}

func resolvePATExpiry(expiresInDays *int) *time.Time {
	if expiresInDays == nil || *expiresInDays <= 0 {
		return nil
	}
	t := time.Now().AddDate(0, 0, *expiresInDays)
	return &t
}

func (s *PersonalAccessTokenService) List(ctx context.Context, tenantID, userID uuid.UUID) ([]domain.PersonalAccessToken, error) {
	var out []domain.PersonalAccessToken
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.tokens.ListForUser(ctx, tx, userID)
		out = v
		return err
	})
	return out, err
}

// Revoke is scoped to the caller's own userID -- see
// PersonalAccessTokenRepository.Revoke's WHERE clause, the actual
// authorization boundary here.
func (s *PersonalAccessTokenService) Revoke(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.tokens.Revoke(ctx, tx, id, userID)
	})
}

// Resolve implements middleware.PATResolver -- looks up a bearer token
// presented on /api/v1/**, returning the permissions it currently grants.
// ok=false (not an error) covers every "this isn't a valid, live token"
// case: unknown hash, revoked, expired, or the owning user deactivated
// since the token was issued.
//
// The tenant isn't known from the request itself (that's what this call
// determines) -- resolves the single tenant every deployment has first,
// same reasoning and same helper AuthService.ResolveDefaultTenant uses for
// POST /auth/refresh, then looks up the token inside that tenant's RLS
// scope like everything else.
func (s *PersonalAccessTokenService) Resolve(ctx context.Context, token string) (tenantID, userID uuid.UUID, isAdmin bool, resourceAccess, allowedTags []string, ok bool, err error) {
	tenant, err := s.tenants.GetDefault(ctx, s.pool)
	if err != nil {
		return uuid.Nil, uuid.Nil, false, nil, nil, false, fmt.Errorf("resolve default tenant: %w", err)
	}
	if tenant == nil {
		return uuid.Nil, uuid.Nil, false, nil, nil, false, nil
	}

	var user *domain.User
	err = s.pool.WithTenant(ctx, tenant.ID, func(tx pgx.Tx) error {
		pat, err := s.tokens.GetByHash(ctx, tx, hashPAT(token))
		if err != nil {
			return fmt.Errorf("load personal access token: %w", err)
		}
		if pat == nil || pat.RevokedAt != nil || (pat.ExpiresAt != nil && pat.ExpiresAt.Before(time.Now())) {
			return nil
		}

		u, err := s.users.Get(ctx, tx, pat.UserID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || !u.IsActive {
			return nil
		}
		user = u

		return s.tokens.StampLastUsed(ctx, tx, pat.ID)
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, false, nil, nil, false, err
	}
	if user == nil {
		return uuid.Nil, uuid.Nil, false, nil, nil, false, nil
	}

	return tenant.ID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, true, nil
}
