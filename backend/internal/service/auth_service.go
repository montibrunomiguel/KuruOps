package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// AuthService is where local, LDAP, and SAML login all converge on the same
// two outputs: a domain.User (created/updated as needed) and a signed
// session JWT. See architecture review, "Auth: local + LDAP + SAML".
type AuthService struct {
	pool          *db.Pool
	tenants       *repository.TenantRepository
	users         *repository.UserRepository
	refreshTokens *repository.RefreshTokenRepository
	roles         *RoleService
	issuer        *authn.Issuer
}

func NewAuthService(pool *db.Pool, tenants *repository.TenantRepository, users *repository.UserRepository, refreshTokens *repository.RefreshTokenRepository, roles *RoleService, issuer *authn.Issuer) *AuthService {
	return &AuthService{pool: pool, tenants: tenants, users: users, refreshTokens: refreshTokens, roles: roles, issuer: issuer}
}

// refreshTokenTTL is how long a refresh token stays valid after issuance or
// rotation -- deliberately much longer than the 15-minute access token
// (authn.Issuer), since re-authenticating every 15 minutes would make the
// short access-token TTL pointless from a usability standpoint. The
// tradeoff is bounded by RevokeSessions, not by a short TTL here.
const refreshTokenTTL = 30 * 24 * time.Hour

const refreshTokenPrefix = "rt_"

func generateRefreshToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return refreshTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// issueRefreshToken generates and persists a new refresh token for a user
// inside an already-open tenant-scoped transaction, returning the plaintext
// -- only ever returned here, never retrievable again (same discipline as
// WebhookService's token issuance).
func (s *AuthService) issueRefreshToken(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID) (string, error) {
	plaintext, err := generateRefreshToken()
	if err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	rt := &domain.RefreshToken{
		TenantID:  tenantID,
		UserID:    userID,
		TokenHash: hashRefreshToken(plaintext),
		ExpiresAt: time.Now().Add(refreshTokenTTL),
	}
	if err := s.refreshTokens.Insert(ctx, tx, rt); err != nil {
		return "", fmt.Errorf("insert refresh token: %w", err)
	}
	return plaintext, nil
}

// ResolveDefaultTenant is the first step of every login flow: resolve the
// single tenant every deployment has, before app.tenant_id can be set (see
// TenantRepository.GetDefault).
func (s *AuthService) ResolveDefaultTenant(ctx context.Context) (*domain.Tenant, error) {
	return s.tenants.GetDefault(ctx, s.pool)
}

// LoginLocal verifies email+password against the users table for
// auth_provider='local'. Returns (nil, nil, nil) — not an error — for
// unknown email or wrong password alike, so callers can't distinguish
// "no such user" from "wrong password" through error type/message, which
// would let a login form enumerate valid emails.
func (s *AuthService) LoginLocal(ctx context.Context, tenantID uuid.UUID, email, password string) (*domain.User, string, string, error) {
	var user *domain.User
	var refreshToken string
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.GetByEmail(ctx, tx, email)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || u.AuthProvider != domain.AuthProviderLocal || !u.IsActive || u.PasswordHash == nil {
			return nil
		}

		ok, err := authn.VerifyPassword(*u.PasswordHash, password)
		if err != nil || !ok {
			return nil
		}

		if err := s.users.StampLastLogin(ctx, tx, u.ID); err != nil {
			return fmt.Errorf("stamp last login: %w", err)
		}
		rt, err := s.issueRefreshToken(ctx, tx, tenantID, u.ID)
		if err != nil {
			return err
		}
		refreshToken = rt
		user = u
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}
	if user == nil {
		return nil, "", "", nil
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, user.MustChangePassword)
	if err != nil {
		return nil, "", "", fmt.Errorf("issue token: %w", err)
	}
	return user, token, refreshToken, nil
}

// Refresh exchanges a valid, unrevoked refresh token for a new access
// token, rotating the refresh token itself in the same transaction (the old
// one is revoked the instant its replacement is issued, so a stolen-and-
// replayed old token after a legitimate refresh is rejected same as any
// other revoked token).
func (s *AuthService) Refresh(ctx context.Context, tenantID uuid.UUID, refreshToken string) (string, string, error) {
	var user *domain.User
	var newRefreshToken string
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rt, err := s.refreshTokens.GetByHash(ctx, tx, hashRefreshToken(refreshToken))
		if err != nil {
			return fmt.Errorf("load refresh token: %w", err)
		}
		if rt == nil || rt.RevokedAt != nil || rt.ExpiresAt.Before(time.Now()) {
			return nil
		}

		u, err := s.users.Get(ctx, tx, rt.UserID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || !u.IsActive {
			return nil
		}

		if err := s.refreshTokens.Revoke(ctx, tx, rt.ID); err != nil {
			return fmt.Errorf("revoke used refresh token: %w", err)
		}
		next, err := s.issueRefreshToken(ctx, tx, tenantID, u.ID)
		if err != nil {
			return err
		}
		newRefreshToken = next
		user = u
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if user == nil {
		return "", "", nil
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, user.MustChangePassword)
	if err != nil {
		return "", "", fmt.Errorf("issue token: %w", err)
	}
	return token, newRefreshToken, nil
}

// RevokeSessions invalidates every outstanding refresh token for a user --
// called on deactivation and the admin "Revoke sessions" action. The
// user's current access token (if any) still works until its own 15-minute
// expiry; this only stops it from being renewed.
func (s *AuthService) RevokeSessions(ctx context.Context, tenantID, userID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.refreshTokens.RevokeAllForUser(ctx, tx, userID)
	})
}

// ChangePassword verifies currentPassword against the stored hash (even
// when the account is locked to this endpoint by MustChangePassword --
// knowing the default password is not itself proof of authorization, only
// a valid session token plus the current password is), then rotates it and
// re-issues a token with MustChangePassword cleared so the frontend can
// swap it in immediately instead of forcing a fresh login.
func (s *AuthService) ChangePassword(ctx context.Context, tenantID, userID uuid.UUID, currentPassword, newPassword string) (string, error) {
	if len(newPassword) < 8 {
		return "", fmt.Errorf("new password must be at least 8 characters")
	}

	var user *domain.User
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, userID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || u.AuthProvider != domain.AuthProviderLocal || u.PasswordHash == nil {
			return fmt.Errorf("password change is only available for local accounts")
		}
		ok, err := authn.VerifyPassword(*u.PasswordHash, currentPassword)
		if err != nil || !ok {
			return fmt.Errorf("current password is incorrect")
		}

		newHash, err := authn.HashPassword(newPassword)
		if err != nil {
			return fmt.Errorf("hash new password: %w", err)
		}
		if err := s.users.SetPassword(ctx, tx, userID, newHash); err != nil {
			return fmt.Errorf("set password: %w", err)
		}
		u.MustChangePassword = false
		user = u
		return nil
	})
	if err != nil {
		return "", err
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, false)
	if err != nil {
		return "", fmt.Errorf("issue token: %w", err)
	}
	return token, nil
}

// UpdateProfile lets a local-auth user change their own name/email. A name
// change is free; an email change is a login-identity change, not cosmetic,
// so it requires currentPassword confirmation -- a valid session isn't
// itself proof of intent to change identity, same reasoning ChangePassword
// already documents for password changes. LDAP/SAML users are rejected:
// their name/email come from the IdP and are overwritten on every login
// anyway, so editing them here would just be silently undone.
func (s *AuthService) UpdateProfile(ctx context.Context, tenantID, userID uuid.UUID, name, email, currentPassword string) (*domain.User, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}

	var user *domain.User
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, userID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || u.AuthProvider != domain.AuthProviderLocal {
			return fmt.Errorf("profile changes are only available for local accounts")
		}

		if email != u.Email {
			if u.PasswordHash == nil {
				return fmt.Errorf("current password is incorrect")
			}
			ok, err := authn.VerifyPassword(*u.PasswordHash, currentPassword)
			if err != nil || !ok {
				return fmt.Errorf("current password is incorrect")
			}
		}

		if err := s.users.UpdateProfile(ctx, tx, userID, name, email); err != nil {
			return err
		}
		u.Name = name
		u.Email = email
		user = u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ProvisionFederated is the just-in-time provisioning step shared by the
// LDAP and SAML flows: given the external identity (email, display name,
// provider, external id) and the groups the IdP reported, resolve a Role
// from AuthGroupMapping, upsert the user, and issue a session token. The
// first matching mapping wins if the user belongs to more than one mapped
// group -- there is no "most privileged wins" merge in v1, callers
// configure mappings accordingly. A user whose groups match no mapping
// gets RoleService.EnsureUnmappedFallback's least-privilege role instead of
// being rejected -- see that method's doc comment.
func (s *AuthService) ProvisionFederated(ctx context.Context, tenantID uuid.UUID, provider domain.AuthProvider, externalID, email, name string, groups []string) (*domain.User, string, string, error) {
	var user *domain.User
	var refreshToken string
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		mappings, err := s.users.ListGroupMappings(ctx, tx)
		if err != nil {
			return fmt.Errorf("load group mappings: %w", err)
		}

		var roleID uuid.UUID
	matchGroups:
		for _, group := range groups {
			for _, m := range mappings {
				if m.Provider == provider && m.ExternalGroup == group {
					roleID = m.RoleID
					break matchGroups
				}
			}
		}
		if roleID == uuid.Nil {
			roleID, err = s.roles.EnsureUnmappedFallback(ctx, tx, tenantID)
			if err != nil {
				return fmt.Errorf("resolve fallback role: %w", err)
			}
		}

		u := &domain.User{
			TenantID:     tenantID,
			Email:        email,
			Name:         name,
			AuthProvider: provider,
			ExternalID:   &externalID,
			RoleID:       roleID,
		}
		if err := s.users.UpsertFederated(ctx, tx, u); err != nil {
			return err
		}
		rt, err := s.issueRefreshToken(ctx, tx, tenantID, u.ID)
		if err != nil {
			return err
		}
		refreshToken = rt

		// UpsertFederated only returns id/is_active/timestamps, not the
		// joined Role -- reload so the token is issued from the real,
		// current capability set (also covers "existing federated user,
		// mapping just changed", not only first-ever provisioning).
		reloaded, err := s.users.Get(ctx, tx, u.ID)
		if err != nil {
			return fmt.Errorf("reload provisioned user: %w", err)
		}
		user = reloaded
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, false)
	if err != nil {
		return nil, "", "", fmt.Errorf("issue token: %w", err)
	}
	return user, token, refreshToken, nil
}
