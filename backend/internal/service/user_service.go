package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

type UserService struct {
	pool *db.Pool
	repo *repository.UserRepository
}

func NewUserService(pool *db.Pool, repo *repository.UserRepository) *UserService {
	return &UserService{pool: pool, repo: repo}
}

func (s *UserService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.User, error) {
	var users []domain.User
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		users = v
		return err
	})
	return users, err
}

// ListSummaries backs GET /api/v1/users/directory -- see domain.UserSummary
// for why this is a separate, non-admin-gated method rather than reusing
// List (which returns full domain.User, including its Role).
func (s *UserService) ListSummaries(ctx context.Context, tenantID uuid.UUID) ([]domain.UserSummary, error) {
	var summaries []domain.UserSummary
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.ListSummaries(ctx, tx)
		summaries = v
		return err
	})
	return summaries, err
}

// Get is used internally (e.g. resolving the acting user's display name for
// IncidentService.AddComment) rather than exposed as its own admin-gated
// route -- see router.go, where /settings/users stays List-only.
func (s *UserService) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.User, error) {
	var user *domain.User
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.Get(ctx, tx, id)
		user = v
		return err
	})
	return user, err
}

// CreateLocal provisions a brand-new local-auth user from Settings -> Users
// & Roles ("+ New User") -- the admin-facing counterpart to
// UpsertFederated, which only ever runs during an LDAP/SAML login. Unlike
// federated provisioning, there's no external identity source to trust, so
// this generates a random one-time password instead of taking one from the
// admin: the admin shares it out of band, and must_change_password forces
// the new user to set their own on first login (same flow as the seeded
// default admin from 0013_seed_default_admin.up.sql). The plaintext
// password is returned once here and never stored or logged anywhere else.
func (s *UserService) CreateLocal(ctx context.Context, tenantID uuid.UUID, email, name, phone string, roleID uuid.UUID) (*domain.User, string, error) {
	email = strings.TrimSpace(email)
	name = strings.TrimSpace(name)
	phone = strings.TrimSpace(phone)
	if email == "" {
		return nil, "", fmt.Errorf("email is required")
	}
	if name == "" {
		return nil, "", fmt.Errorf("name is required")
	}
	if err := domain.ValidatePhone(phone); err != nil {
		return nil, "", err
	}
	var phonePtr *string
	if phone != "" {
		phonePtr = &phone
	}

	tempPassword, err := generateTempPassword()
	if err != nil {
		return nil, "", fmt.Errorf("generate temp password: %w", err)
	}
	hash, err := authn.HashPassword(tempPassword)
	if err != nil {
		return nil, "", fmt.Errorf("hash temp password: %w", err)
	}

	u := &domain.User{
		TenantID:     tenantID,
		Email:        email,
		Name:         name,
		AuthProvider: domain.AuthProviderLocal,
		RoleID:       roleID,
		Phone:        phonePtr,
	}
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.CreateLocal(ctx, tx, u, hash)
	})
	if err != nil {
		return nil, "", fmt.Errorf("create user: %w", err)
	}
	return u, tempPassword, nil
}

// generateTempPassword returns a random URL-safe string with enough entropy
// to stand in as a one-time password -- it's never typed, only copy-pasted
// by the admin and immediately rotated by the new user via the forced
// change-password flow, so readability doesn't matter, only randomness.
func generateTempPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// UpdateAccess changes which Role a user is assigned -- the field editable
// from Settings -> Users & Roles. Email/name/auth_provider are not editable
// here: they come from the identity source (local signup or the LDAP/SAML
// provisioning flow in AuthGroupMapping), not from an admin hand-editing a
// user record.
func (s *UserService) UpdateAccess(ctx context.Context, tenantID, id, roleID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.UpdateAccess(ctx, tx, id, roleID)
	})
}

// UpdatePhone is the admin "edit an existing user's phone" action --
// Settings -> Users & Roles has no general edit-user form (see
// UpdateAccess's own doc comment on why name/email aren't editable there),
// but phone is ArgusOps-local metadata, not identity-sourced, so an admin
// can set or clear it for any user regardless of auth provider.
func (s *UserService) UpdatePhone(ctx context.Context, tenantID, id uuid.UUID, phone string) error {
	phone = strings.TrimSpace(phone)
	if err := domain.ValidatePhone(phone); err != nil {
		return err
	}
	var phonePtr *string
	if phone != "" {
		phonePtr = &phone
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.UpdatePhone(ctx, tx, id, phonePtr)
	})
}

// ResetPassword is the admin "reset a user's password" action --
// same one-time-temp-password shape as CreateLocal, reusing
// generateTempPassword so both flows share the exact same entropy/format.
// Only local-auth users have a password to reset; LDAP/SAML users
// authenticate against their identity provider, so resetting a local
// password here would be a no-op that misleads the admin into thinking it
// did something.
func (s *UserService) ResetPassword(ctx context.Context, tenantID, userID uuid.UUID) (string, error) {
	user, err := s.Get(ctx, tenantID, userID)
	if err != nil {
		return "", fmt.Errorf("load user: %w", err)
	}
	if user == nil {
		return "", fmt.Errorf("user not found")
	}
	if user.AuthProvider != domain.AuthProviderLocal {
		return "", fmt.Errorf("cannot reset password for a %s-authenticated user", user.AuthProvider)
	}

	tempPassword, err := generateTempPassword()
	if err != nil {
		return "", fmt.Errorf("generate temp password: %w", err)
	}
	hash, err := authn.HashPassword(tempPassword)
	if err != nil {
		return "", fmt.Errorf("hash temp password: %w", err)
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.SetPasswordAndForceChange(ctx, tx, userID, hash)
	})
	if err != nil {
		return "", fmt.Errorf("reset password: %w", err)
	}
	return tempPassword, nil
}

func (s *UserService) SetActive(ctx context.Context, tenantID, id uuid.UUID, active bool) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.SetActive(ctx, tx, id, active)
	})
}

func (s *UserService) ListGroupMappings(ctx context.Context, tenantID uuid.UUID) ([]domain.AuthGroupMapping, error) {
	var mappings []domain.AuthGroupMapping
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.ListGroupMappings(ctx, tx)
		mappings = v
		return err
	})
	return mappings, err
}

// SaveGroupMapping creates or replaces the Role mapping for one LDAP group
// or SAML attribute value. Applied by AuthService.ProvisionFederated every
// time a federated user authenticates — see architecture review, "Auth:
// local + LDAP + SAML", for why this is just-in-time rather than a one-time
// import.
func (s *UserService) SaveGroupMapping(ctx context.Context, tenantID uuid.UUID, provider domain.AuthProvider, externalGroup string, roleID uuid.UUID) (*domain.AuthGroupMapping, error) {
	if provider != domain.AuthProviderLDAP && provider != domain.AuthProviderSAML {
		return nil, fmt.Errorf("group mappings only apply to ldap or saml, got %q", provider)
	}
	m := &domain.AuthGroupMapping{
		TenantID:      tenantID,
		Provider:      provider,
		ExternalGroup: externalGroup,
		RoleID:        roleID,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.UpsertGroupMapping(ctx, tx, m)
	})
	if err != nil {
		return nil, fmt.Errorf("save group mapping: %w", err)
	}
	return m, nil
}

func (s *UserService) DeleteGroupMapping(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.DeleteGroupMapping(ctx, tx, id)
	})
}
