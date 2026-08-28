package authn

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// tokenTTL is deliberately short: this JWT is a session token, not a
// long-lived API key. A revoked user or a role/tag change from Settings ->
// Users & Roles takes effect within this window, not immediately, since
// nothing here checks a revocation list on every request (see
// backend/README.md for the tradeoff and what a production hardening pass
// would add — token introspection or a short-TTL + refresh-token pair).
const tokenTTL = 15 * time.Minute

// Claims is what every login path (local, LDAP, SAML) converges on — see
// architecture review, "Auth: local + LDAP + SAML". httpserver/middleware
// reads the same shape out of the verified token.
type Claims struct {
	jwt.RegisteredClaims
	TenantID uuid.UUID `json:"tenant_id"`
	UserID   uuid.UUID `json:"user_id"`
	// IsAdmin mirrors the user's Role.IsAdmin at the moment of login --
	// what gates Settings access (see middleware.RequireAdmin).
	IsAdmin bool `json:"is_admin"`
	// ResourceAccess and AllowedTags mirror the user's Role at the moment of
	// login -- carrying them in the token means every request can enforce
	// the tag-based/resource-based scoping model (see design handoff,
	// "Tag-based + resource-based access scoping") without a DB round trip
	// per request. Same staleness tradeoff as IsAdmin: a change in Settings
	// -> Users & Roles takes effect on next login/token refresh, not
	// immediately (see tokenTTL).
	ResourceAccess []string `json:"resource_access"`
	AllowedTags    []string `json:"allowed_tags"`
	// MustChangePassword locks the holder to POST /account/change-password
	// only (see middleware.RequirePasswordChanged) -- true for a fresh
	// default-admin login until they rotate it.
	MustChangePassword bool `json:"must_change_password"`
	// MFAEnabled mirrors users.mfa_totp_secret != nil at the moment of
	// issuance -- the frontend's Profile page reads this (not a dedicated
	// GET endpoint, there isn't one) to show 2FA as on/off, same staleness
	// tradeoff as IsAdmin/ResourceAccess: it only actually updates on next
	// login/token refresh, which is why ConfirmMFA/DisableMFA both re-issue
	// a fresh token the frontend swaps in immediately (see AuthContext.applyNewToken).
	MFAEnabled bool `json:"mfa_enabled"`
}

// Issuer signs session tokens. Only cmd/api's login handlers hold the
// private key — cmd/ingest and cmd/worker never need to issue tokens, only
// (eventually) verify them, so they only need the public key.
type Issuer struct {
	privateKey *rsa.PrivateKey
}

func NewIssuer(privateKey *rsa.PrivateKey) *Issuer {
	return &Issuer{privateKey: privateKey}
}

func (i *Issuer) Issue(tenantID, userID uuid.UUID, isAdmin bool, resourceAccess []string, allowedTags []string, mustChangePassword, mfaEnabled bool) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
			Issuer:    "kuruops",
		},
		TenantID:           tenantID,
		UserID:             userID,
		IsAdmin:            isAdmin,
		ResourceAccess:     resourceAccess,
		AllowedTags:        allowedTags,
		MustChangePassword: mustChangePassword,
		MFAEnabled:         mfaEnabled,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(i.privateKey)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// Verifier checks session tokens. Holds only the public key, so a service
// that verifies but never issues (cmd/ingest, cmd/worker, if they ever need
// to authenticate a user-driven request) can't be tricked into signing.
type Verifier struct {
	publicKey *rsa.PublicKey
}

func NewVerifier(publicKey *rsa.PublicKey) *Verifier {
	return &Verifier{publicKey: publicKey}
}

func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	var claims Claims
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return v.publicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return &claims, nil
}
