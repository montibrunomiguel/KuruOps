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

type WebhookService struct {
	pool *db.Pool
	repo *repository.WebhookRepository
}

func NewWebhookService(pool *db.Pool, repo *repository.WebhookRepository) *WebhookService {
	return &WebhookService{pool: pool, repo: repo}
}

func (s *WebhookService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.WebhookEndpoint, error) {
	var endpoints []domain.WebhookEndpoint
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		endpoints = v
		return err
	})
	return endpoints, err
}

// CreateResult carries the plaintext token exactly once — it is never
// stored (only its hash is) and never retrievable again after this
// response, matching the "Copy URL" masked-token pattern in Settings.
type CreateResult struct {
	Endpoint domain.WebhookEndpoint
	Token    string
}

// Create issues a new webhook endpoint + token. expiresInDays follows the
// same convention as Regenerate (see resolveExpiry): nil defaults to the
// standard 90-day rotation policy, 0/negative means the admin explicitly
// opted this endpoint out of expiring. fieldMappingTemplateID is optional
// and, unlike name/source, can be changed later via SetFieldMappingTemplate.
func (s *WebhookService) Create(ctx context.Context, tenantID, actorID uuid.UUID, name, source string, expiresInDays *int, fieldMappingTemplateID *uuid.UUID) (*CreateResult, error) {
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	ep := &domain.WebhookEndpoint{
		TenantID:               tenantID,
		Name:                   name,
		Source:                 source,
		TokenHash:              hashToken(token),
		TokenLast4:             lastN(token, 4),
		ExpiresAt:              resolveExpiry(expiresInDays),
		CreatedBy:              &actorID,
		FieldMappingTemplateID: fieldMappingTemplateID,
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, ep)
	})
	if err != nil {
		return nil, fmt.Errorf("create webhook endpoint: %w", err)
	}

	return &CreateResult{Endpoint: *ep, Token: token}, nil
}

// Regenerate issues a new token for an existing endpoint, invalidating the
// old one immediately — "Regenerate" in Settings -> Webhook Endpoints.
// Rotating also resets the expiry clock (see resolveExpiry).
func (s *WebhookService) Regenerate(ctx context.Context, tenantID, id uuid.UUID, expiresInDays *int) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.RotateToken(ctx, tx, id, hashToken(token), lastN(token, 4), resolveExpiry(expiresInDays))
	})
	if err != nil {
		return "", fmt.Errorf("regenerate token: %w", err)
	}
	return token, nil
}

// defaultTokenExpiryDays is the rotation policy applied when the caller
// doesn't say otherwise: a webhook token expires 90 days after it was
// created or last regenerated, so a forgotten integration's credential
// doesn't stay valid forever. An admin can explicitly opt an endpoint out
// by passing 0 (or a negative number), which resolveExpiry treats as
// "never expires" (nil).
const defaultTokenExpiryDays = 90

func resolveExpiry(expiresInDays *int) *time.Time {
	days := defaultTokenExpiryDays
	if expiresInDays != nil {
		if *expiresInDays <= 0 {
			return nil
		}
		days = *expiresInDays
	}
	t := time.Now().AddDate(0, 0, days)
	return &t
}

func (s *WebhookService) SetStatus(ctx context.Context, tenantID, id uuid.UUID, status string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("invalid status %q", status)
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.SetStatus(ctx, tx, id, status)
	})
}

// SetFieldMappingTemplate assigns or clears (templateID == nil) which
// field mapping template applies to alerts this endpoint ingests from now
// on -- see Settings -> Webhook Endpoints' "change template" action.
func (s *WebhookService) SetFieldMappingTemplate(ctx context.Context, tenantID, id uuid.UUID, templateID *uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.SetFieldMappingTemplate(ctx, tx, id, templateID)
	})
}

const tokenPrefix = "whk_"

func generateToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
