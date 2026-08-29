package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"

	"github.com/kuruops/kuruops/internal/authn"
	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
)

// totpIssuer is the "issuer" label an authenticator app (Google
// Authenticator, 1Password, ...) shows next to the account name once a
// QR code from GenerateMFAEnrollment is scanned -- cosmetic only, does not
// affect verification.
const totpIssuer = "KuruOps"

// AuthService is where local, LDAP, and SAML login all converge on the same
// two outputs: a domain.User (created/updated as needed) and a signed
// session JWT. See architecture review, "Auth: local + LDAP + SAML".
type AuthService struct {
	pool          *db.Pool
	tenants       *repository.TenantRepository
	users         *repository.UserRepository
	refreshTokens *repository.RefreshTokenRepository
	mfaPending    *repository.MFAPendingTokenRepository
	roles         *RoleService
	issuer        *authn.Issuer
	// secrets stores each user's TOTP secret via secrets.Store, the same
	// boundary every other credential-class secret in this codebase (LLM
	// API keys, MCP auth tokens, the SAML SP key, ...) already crosses --
	// see GenerateMFAEnrollment/ConfirmMFA/VerifyMFA. users.mfa_totp_secret
	// holds only the opaque ref this returns, never the raw secret.
	secrets secrets.Store
}

func NewAuthService(pool *db.Pool, tenants *repository.TenantRepository, users *repository.UserRepository, refreshTokens *repository.RefreshTokenRepository, mfaPending *repository.MFAPendingTokenRepository, roles *RoleService, issuer *authn.Issuer, store secrets.Store) *AuthService {
	return &AuthService{pool: pool, tenants: tenants, users: users, refreshTokens: refreshTokens, mfaPending: mfaPending, roles: roles, issuer: issuer, secrets: store}
}

// mfaSecretPurpose is the secrets.Store purpose key for a user's TOTP
// secret -- namespaced per-user (unlike "llm:"+name or "mcp:"+name, which
// are unique enough within a tenant on their own) since two different
// users' otherwise-identically-named enrollments must never collide on the
// same ref.
func mfaSecretPurpose(userID uuid.UUID) string {
	return "mfa-totp:" + userID.String()
}

// refreshTokenTTL is how long a refresh token stays valid after issuance or
// rotation -- deliberately much longer than the 15-minute access token
// (authn.Issuer), since re-authenticating every 15 minutes would make the
// short access-token TTL pointless from a usability standpoint. The
// tradeoff is bounded by RevokeSessions, not by a short TTL here.
const refreshTokenTTL = 30 * 24 * time.Hour

const refreshTokenPrefix = "rt_"

// issueRefreshToken generates and persists a new refresh token for a user
// inside an already-open tenant-scoped transaction, returning the plaintext
// -- only ever returned here, never retrievable again (same discipline as
// WebhookService's token issuance).
func (s *AuthService) issueRefreshToken(ctx context.Context, tx pgx.Tx, tenantID, userID uuid.UUID) (string, error) {
	plaintext, err := generatePrefixedToken(refreshTokenPrefix, 32)
	if err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	rt := &domain.RefreshToken{
		TenantID:  tenantID,
		UserID:    userID,
		TokenHash: hashToken(plaintext),
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

// mfaPendingTokenTTL bounds how long a caller has to type the 6-digit code
// after a correct password, before having to log in again from scratch --
// short and single-use like oauth_states' TTL, not password reset's 1-hour
// one: this only needs to survive the few seconds it takes to read a code
// already showing on an authenticator app.
const mfaPendingTokenTTL = 10 * time.Minute

const mfaPendingTokenPrefix = "mfap_"

// LoginLocal verifies email+password against the users table for
// auth_provider='local'. Returns (nil, "", "", "", nil) — not an error —
// for unknown email or wrong password alike, so callers can't distinguish
// "no such user" from "wrong password" through error type/message, which
// would let a login form enumerate valid emails.
//
// A user with TOTP enrolled (MFATOTPSecret set) never gets a session from
// this call alone: on a correct password it mints an mfa_pending_tokens row
// instead and returns its plaintext as pendingToken, with user/token/
// refreshToken all zero -- the caller must present that pendingToken plus
// a valid code to VerifyMFA to actually complete the login. completeLogin
// (stamp last login, issue refresh token, issue the session JWT) is shared
// by both this method's no-MFA path and VerifyMFA's success path, so a
// session is issued identically either way.
func (s *AuthService) LoginLocal(ctx context.Context, tenantID uuid.UUID, email, password string) (user *domain.User, token, refreshToken, pendingToken string, err error) {
	var verifiedUser *domain.User
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.GetByEmail(ctx, tx, tenantID, email)
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

		if u.MFATOTPSecret != nil {
			plaintext, err := generatePrefixedToken(mfaPendingTokenPrefix, 32)
			if err != nil {
				return fmt.Errorf("generate mfa pending token: %w", err)
			}
			mp := &domain.MFAPendingToken{
				TenantID:  tenantID,
				UserID:    u.ID,
				TokenHash: hashToken(plaintext),
				ExpiresAt: time.Now().Add(mfaPendingTokenTTL),
			}
			if err := s.mfaPending.Insert(ctx, tx, mp); err != nil {
				return fmt.Errorf("insert mfa pending token: %w", err)
			}
			pendingToken = plaintext
			return nil
		}

		verifiedUser = u
		return nil
	})
	if err != nil {
		return nil, "", "", "", err
	}
	if pendingToken != "" {
		return nil, "", "", pendingToken, nil
	}
	if verifiedUser == nil {
		return nil, "", "", "", nil
	}

	user, token, refreshToken, err = s.completeLogin(ctx, tenantID, verifiedUser)
	if err != nil {
		return nil, "", "", "", err
	}
	return user, token, refreshToken, "", nil
}

// VerifyMFA is the second leg of a login for a TOTP-enrolled user: consumes
// pendingToken (single-use, see MFAPendingTokenRepository.ConsumeByHash),
// loads the user it belongs to, and checks code against their enrolled
// secret. Same opaque-failure discipline as LoginLocal -- an unknown/
// expired/already-used pendingToken and a wrong code are indistinguishable
// to the caller, both just (nil, "", "", nil).
func (s *AuthService) VerifyMFA(ctx context.Context, tenantID uuid.UUID, pendingToken, code string) (*domain.User, string, string, error) {
	var verifiedUser *domain.User
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		mp, err := s.mfaPending.GetByHash(ctx, tx, hashToken(pendingToken))
		if err != nil {
			return fmt.Errorf("load mfa pending token: %w", err)
		}
		if mp == nil {
			return nil
		}

		u, err := s.users.Get(ctx, tx, tenantID, mp.UserID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || !u.IsActive || u.MFATOTPSecret == nil {
			return nil
		}

		// users.mfa_totp_secret holds a secrets.Store ref, not the raw
		// secret -- see ConfirmMFA.
		secret, err := s.secrets.Resolve(ctx, *u.MFATOTPSecret)
		if err != nil {
			return fmt.Errorf("resolve mfa secret: %w", err)
		}
		if !totp.Validate(code, secret) {
			return nil
		}
		// Only burn the pending token once the code has actually checked
		// out -- see MFAPendingTokenRepository.GetByHash's doc comment for
		// why a wrong code must not invalidate it.
		if err := s.mfaPending.MarkConsumed(ctx, tx, mp.ID); err != nil {
			return fmt.Errorf("mark mfa pending token consumed: %w", err)
		}
		verifiedUser = u
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}
	if verifiedUser == nil {
		return nil, "", "", nil
	}
	return s.completeLogin(ctx, tenantID, verifiedUser)
}

// completeLogin is the tail end every successful login (local, non-MFA;
// local, post-MFA) shares: stamp last login and issue a refresh token
// inside one transaction, then issue the session JWT once it's committed --
// the exact steps LoginLocal used to inline itself before MFA gave it a
// second path to the same destination.
func (s *AuthService) completeLogin(ctx context.Context, tenantID uuid.UUID, user *domain.User) (*domain.User, string, string, error) {
	var refreshToken string
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.users.StampLastLogin(ctx, tx, user.ID); err != nil {
			return fmt.Errorf("stamp last login: %w", err)
		}
		rt, err := s.issueRefreshToken(ctx, tx, tenantID, user.ID)
		if err != nil {
			return err
		}
		refreshToken = rt
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, user.MustChangePassword, user.MFATOTPSecret != nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("issue token: %w", err)
	}
	return user, token, refreshToken, nil
}

// GenerateMFAEnrollment creates a fresh random TOTP secret and its
// otpauth:// URI for the caller to render as a QR code -- deliberately NOT
// persisted here. The secret only reaches users.mfa_totp_secret via
// ConfirmMFA, once the caller has proven they actually captured it (by
// producing a code from it) -- generating and saving in one step here would
// let a scan the user never completed (interrupted before scanning, wrong
// QR entirely) silently lock their account behind an authenticator app that
// never has the right secret.
func (s *AuthService) GenerateMFAEnrollment(ctx context.Context, tenantID, userID uuid.UUID) (secret, otpauthURL string, err error) {
	var email string
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, tenantID, userID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil {
			return fmt.Errorf("user not found")
		}
		email = u.Email
		return nil
	})
	if err != nil {
		return "", "", err
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: email})
	if err != nil {
		return "", "", fmt.Errorf("generate totp key: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ConfirmMFA activates 2FA for userID: re-validates code against the
// caller-supplied secret (proof the enrollment's QR/manual-entry secret was
// actually captured by a real authenticator app, not just round-tripped
// blind) before persisting it to users.mfa_totp_secret. Returns a freshly
// issued token with mfa_enabled now true -- same reasoning ChangePassword's
// doc comment gives for re-issuing after a mutation that changes what the
// current token's claims should say: the frontend swaps it in immediately
// (AuthContext.applyNewToken) instead of the change only showing up after
// the next login/refresh.
func (s *AuthService) ConfirmMFA(ctx context.Context, tenantID, userID uuid.UUID, secret, code string) (string, error) {
	if !totp.Validate(code, secret) {
		return "", fmt.Errorf("invalid code")
	}

	// Store the raw TOTP secret via secrets.Store and persist only the
	// returned ref -- users.mfa_totp_secret must never hold the secret
	// itself, same discipline as every other credential-class secret in
	// this codebase. mfaSecretPurpose is deterministic per user, so
	// re-enrolling (ConfirmMFA again after a prior DisableMFA) overwrites
	// the same ref rather than accumulating orphaned ones.
	ref, err := s.secrets.Put(ctx, tenantID.String(), mfaSecretPurpose(userID), secret)
	if err != nil {
		return "", fmt.Errorf("store mfa secret: %w", err)
	}

	var user *domain.User
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.users.SetMFASecret(ctx, tx, userID, &ref); err != nil {
			return fmt.Errorf("save mfa secret: %w", err)
		}
		u, err := s.users.Get(ctx, tx, tenantID, userID)
		if err != nil {
			return fmt.Errorf("reload user: %w", err)
		}
		user = u
		return nil
	})
	if err != nil {
		return "", err
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, user.MustChangePassword, true)
	if err != nil {
		return "", fmt.Errorf("issue token: %w", err)
	}
	return token, nil
}

// DisableMFA turns 2FA back off for userID -- requires re-entering the
// current password, same reasoning UpdateProfile's email-change guard
// documents: a valid session alone isn't proof of intent to weaken the
// account's own login requirements, someone at an unlocked, unattended
// session shouldn't be able to strip 2FA silently. Returns a freshly issued
// token with mfa_enabled now false, same reasoning as ConfirmMFA's.
func (s *AuthService) DisableMFA(ctx context.Context, tenantID, userID uuid.UUID, currentPassword string) (string, error) {
	var user *domain.User
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, tenantID, userID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || u.PasswordHash == nil {
			return fmt.Errorf("current password is incorrect")
		}
		ok, err := authn.VerifyPassword(*u.PasswordHash, currentPassword)
		if err != nil || !ok {
			return fmt.Errorf("current password is incorrect")
		}
		if err := s.users.SetMFASecret(ctx, tx, userID, nil); err != nil {
			return fmt.Errorf("clear mfa secret: %w", err)
		}
		u.MFATOTPSecret = nil
		user = u
		return nil
	})
	if err != nil {
		return "", err
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, user.MustChangePassword, false)
	if err != nil {
		return "", fmt.Errorf("issue token: %w", err)
	}
	return token, nil
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
		rt, err := s.refreshTokens.GetByHash(ctx, tx, hashToken(refreshToken))
		if err != nil {
			return fmt.Errorf("load refresh token: %w", err)
		}
		if rt == nil || rt.RevokedAt != nil || rt.ExpiresAt.Before(time.Now()) {
			return nil
		}

		u, err := s.users.Get(ctx, tx, tenantID, rt.UserID)
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

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, user.MustChangePassword, user.MFATOTPSecret != nil)
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

// Logout revokes exactly the one refresh token the caller presents -- the
// session logging out -- and deliberately leaves every other refresh token
// for the same user alone (unlike RevokeSessions), since logging out one
// browser/tab shouldn't sign the user out of a session open elsewhere. An
// empty, unknown, or already-revoked token is not an error: logout must
// succeed even when the client's local state is already stale, matching
// LoginLocal's discipline of never letting an auth endpoint's response
// distinguish "valid but already handled" from "never existed".
func (s *AuthService) Logout(ctx context.Context, tenantID uuid.UUID, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rt, err := s.refreshTokens.GetByHash(ctx, tx, hashToken(refreshToken))
		if err != nil {
			return fmt.Errorf("load refresh token: %w", err)
		}
		if rt == nil {
			return nil
		}
		return s.refreshTokens.Revoke(ctx, tx, rt.ID)
	})
}

// ChangePassword verifies currentPassword against the stored hash (even
// when the account is locked to this endpoint by MustChangePassword --
// knowing the default password is not itself proof of authorization, only
// a valid session token plus the current password is), then rotates it and
// re-issues a token with MustChangePassword cleared so the frontend can
// swap it in immediately instead of forcing a fresh login. Also revokes
// every outstanding refresh token for the user (same effect as
// RevokeSessions) -- a password change is exactly the moment a stolen
// refresh token must stop working, otherwise an attacker who captured one
// before the legitimate user noticed and changed their password keeps a
// working session indefinitely. The caller's own current access token
// still works until its own 15-minute expiry (same tradeoff RevokeSessions'
// doc comment already accepts); their next /auth/refresh simply fails and
// they log in again, same as any other device that had a session open.
func (s *AuthService) ChangePassword(ctx context.Context, tenantID, userID uuid.UUID, currentPassword, newPassword string) (string, error) {
	if err := validatePasswordPolicy(newPassword); err != nil {
		return "", err
	}

	var user *domain.User
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		u, err := s.users.Get(ctx, tx, tenantID, userID)
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
		if err := s.refreshTokens.RevokeAllForUser(ctx, tx, userID); err != nil {
			return fmt.Errorf("revoke refresh tokens: %w", err)
		}
		u.MustChangePassword = false
		user = u
		return nil
	})
	if err != nil {
		return "", err
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, false, user.MFATOTPSecret != nil)
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
// phone is nil when the caller's request omitted the field entirely (leave
// the stored value untouched -- see updateProfileRequest); a non-nil empty
// string explicitly clears it; a non-nil non-empty string sets it.
func (s *AuthService) UpdateProfile(ctx context.Context, tenantID, userID uuid.UUID, name, email string, phone *string, currentPassword string) (*domain.User, error) {
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
		u, err := s.users.Get(ctx, tx, tenantID, userID)
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if u == nil || u.AuthProvider != domain.AuthProviderLocal {
			return fmt.Errorf("profile changes are only available for local accounts")
		}

		phonePtr := u.Phone
		if phone != nil {
			trimmed := strings.TrimSpace(*phone)
			if err := domain.ValidatePhone(trimmed); err != nil {
				return err
			}
			if trimmed == "" {
				phonePtr = nil
			} else {
				phonePtr = &trimmed
			}
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

		if err := s.users.UpdateProfile(ctx, tx, userID, name, email, phonePtr); err != nil {
			return err
		}
		u.Name = name
		u.Email = email
		u.Phone = phonePtr
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
		reloaded, err := s.users.Get(ctx, tx, tenantID, u.ID)
		if err != nil {
			return fmt.Errorf("reload provisioned user: %w", err)
		}
		user = reloaded
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}

	token, err := s.issuer.Issue(tenantID, user.ID, user.Role.IsAdmin, user.Role.ResourceAccess, user.Role.AllowedTags, false, user.MFATOTPSecret != nil)
	if err != nil {
		return nil, "", "", fmt.Errorf("issue token: %w", err)
	}
	return user, token, refreshToken, nil
}
