// Package middleware provides what every handler needs before it touches
// domain code: who is calling (auth), which tenant they belong to (tenant
// scoping), and what they're allowed to do (role + tag/resource scope).
// Handlers never read tenant/user/access info from request params or
// bodies — only from the context these middlewares populate, so a client
// can't grant itself broader access by editing a request.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/authn"
)

type ctxKey string

const (
	ctxKeyTenantID           ctxKey = "tenant_id"
	ctxKeyUserID             ctxKey = "user_id"
	ctxKeyRole               ctxKey = "role"
	ctxKeyResourceAccess     ctxKey = "resource_access"
	ctxKeyAllowedTags        ctxKey = "allowed_tags"
	ctxKeyMustChangePassword ctxKey = "must_change_password"
)

// Claims is what the identity broker's JWT carries after local/LDAP/SAML
// login all converge on the same token shape (see architecture review,
// "Auth: local + LDAP + SAML"). Verification (signature, expiry) happens in
// JWTAuth below; this struct is the shape once verified.
type Claims struct {
	TenantID           uuid.UUID
	UserID             uuid.UUID
	Role               string
	ResourceAccess     []string
	AllowedTags        []string
	MustChangePassword bool
}

// JWTAuth verifies the bearer token issued by any of the three login flows
// (local, LDAP, SAML — see internal/httpserver/handlers/auth.go) and
// populates the request context from its claims. This is the real
// production auth path; the dev header bypass below only exists to unblock
// local development before an identity provider is configured.
func JWTAuth(verifier *authn.Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			tokenString, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || tokenString == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}

			claims, err := verifier.Verify(tokenString)
			if err != nil {
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}

			ctx := WithClaims(r.Context(), Claims{
				TenantID:           claims.TenantID,
				UserID:             claims.UserID,
				Role:               claims.Role,
				ResourceAccess:     claims.ResourceAccess,
				AllowedTags:        claims.AllowedTags,
				MustChangePassword: claims.MustChangePassword,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// DevHeaderAuth trusts X-Tenant-ID / X-User-ID / X-Role / X-Resource-Access
// / X-Allowed-Tags request headers verbatim, bypassing JWT verification
// entirely — a token from /auth/.../login will NOT work against this, since
// it never looks at the Authorization header at all. It exists only so
// /api/v1 is curl-able without going through login while building locally —
// it MUST NOT be reachable outside a developer's machine. cmd/api only
// wires this in when AUTH_MODE=dev-headers (never plain "dev", which keeps
// real JWTAuth so a real login still works — see cmd/api/main.go), and that
// must never be set in a deployed environment (see backend/README.md).
// X-Resource-Access is a comma-separated capability list (e.g.
// "alerts,incidents,followup"), defaulting to "alerts,incidents" when
// omitted; X-Allowed-Tags defaults to unrestricted (empty) when omitted, so
// existing curl examples that don't set them keep working. MustChangePassword
// is always false here -- there's no token to carry it, and dev-headers is
// meant to skip login entirely.
func DevHeaderAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := uuid.Parse(r.Header.Get("X-Tenant-ID"))
		if err != nil {
			http.Error(w, "missing/invalid X-Tenant-ID (dev auth mode)", http.StatusUnauthorized)
			return
		}
		userID, err := uuid.Parse(r.Header.Get("X-User-ID"))
		if err != nil {
			http.Error(w, "missing/invalid X-User-ID (dev auth mode)", http.StatusUnauthorized)
			return
		}
		role := r.Header.Get("X-Role")
		if role == "" {
			role = "analyst"
		}
		resourceAccess := []string{"alerts", "incidents"}
		if v := r.Header.Get("X-Resource-Access"); v != "" {
			resourceAccess = strings.Split(v, ",")
		}
		var allowedTags []string
		if v := r.Header.Get("X-Allowed-Tags"); v != "" {
			allowedTags = strings.Split(v, ",")
		}

		ctx := WithClaims(r.Context(), Claims{
			TenantID: tenantID, UserID: userID, Role: role,
			ResourceAccess: resourceAccess, AllowedTags: allowedTags,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole gates a route group to callers whose Role is one of allowed —
// e.g. wrapping /api/v1/settings/** with RequireRole("admin"). Must run
// after JWTAuth/DevHeaderAuth in the middleware chain.
func RequireRole(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, ok := Role(r.Context())
			if !ok {
				http.Error(w, "missing auth context", http.StatusUnauthorized)
				return
			}
			for _, a := range allowed {
				if role == a {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden: this action requires a different role", http.StatusForbidden)
		})
	}
}

// RequireResourceAccess gates a route group (e.g. /api/v1/alerts,
// /api/v1/incidents, or /api/v1/dashboard/followup) to callers whose
// ResourceAccess capability set includes resourceType — the resourceAccess
// half of the design handoff's "Tag-based + resource-based access scoping"
// model. "followup" is gated independently of "alerts"/"incidents" so a SOC
// analyst can be granted the Follow-up view without full access to either —
// see domain.ResourceCapabilityFollowup. The other half, tag-based row
// visibility, can't be a route-level gate since it depends on which records
// exist; see AllowedTags(ctx) and its use in internal/service/access.go.
func RequireResourceAccess(resourceType string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			access, ok := ResourceAccess(r.Context())
			if !ok {
				http.Error(w, "missing auth context", http.StatusUnauthorized)
				return
			}
			for _, a := range access {
				if a == resourceType {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden: your account does not have access to "+resourceType, http.StatusForbidden)
		})
	}
}

// RequirePasswordChanged blocks every /api/v1 route except the one path
// that lets the caller rotate their own password, for as long as
// MustChangePassword is set on their token -- see 0013_seed_default_admin.up.sql
// (the seeded default admin starts in this state) and
// AuthService.ChangePassword (the only thing that clears it). Must run
// after JWTAuth/DevHeaderAuth. changePasswordPath is compared exactly
// (chi's cleaned path), not as a prefix, so it can't be used to smuggle
// access to a route that merely starts with the same string.
func RequirePasswordChanged(changePasswordPath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mustChange, ok := MustChangePassword(r.Context())
			if ok && mustChange && r.URL.Path != changePasswordPath {
				http.Error(w, "forbidden: this account must change its password before doing anything else", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func WithClaims(ctx context.Context, c Claims) context.Context {
	ctx = context.WithValue(ctx, ctxKeyTenantID, c.TenantID)
	ctx = context.WithValue(ctx, ctxKeyUserID, c.UserID)
	ctx = context.WithValue(ctx, ctxKeyRole, c.Role)
	ctx = context.WithValue(ctx, ctxKeyResourceAccess, c.ResourceAccess)
	ctx = context.WithValue(ctx, ctxKeyAllowedTags, c.AllowedTags)
	ctx = context.WithValue(ctx, ctxKeyMustChangePassword, c.MustChangePassword)
	return ctx
}

func TenantID(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(ctxKeyTenantID).(uuid.UUID)
	return v, ok
}

func UserID(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(ctxKeyUserID).(uuid.UUID)
	return v, ok
}

func Role(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyRole).(string)
	return v, ok
}

func ResourceAccess(ctx context.Context) ([]string, bool) {
	v, ok := ctx.Value(ctxKeyResourceAccess).([]string)
	return v, ok
}

// AllowedTags returns the caller's tag scope. An empty (nil or
// zero-length) slice means unrestricted — sees records with any tag —
// matching the semantics of domain.User.AllowedTags.
func AllowedTags(ctx context.Context) []string {
	v, _ := ctx.Value(ctxKeyAllowedTags).([]string)
	return v
}

func MustChangePassword(ctx context.Context) (bool, bool) {
	v, ok := ctx.Value(ctxKeyMustChangePassword).(bool)
	return v, ok
}
