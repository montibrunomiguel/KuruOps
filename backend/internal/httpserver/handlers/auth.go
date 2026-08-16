package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// AuthHandlers serves every login flow (local, LDAP, SAML) that converges
// on service.AuthService issuing a session JWT. Mounted unauthenticated at
// /auth -- see httpserver.NewRouter. ArgusOps is single-instance software
// (see TenantRepository.GetDefault): every handler here resolves the one
// tenant a deployment has instead of taking it from the request, so login
// never asks for a company/tenant name.
type AuthHandlers struct {
	auth          *service.AuthService
	ldap          *service.LDAPAuthService
	saml          *service.SAMLAuthService
	passwordReset *service.PasswordResetService

	// loginAttempts is a second, stricter limiter on top of router.go's
	// per-IP loginLimiter -- keyed by the submitted email instead of the
	// caller's IP, so a distributed brute-force against one account (many
	// source IPs, one target email) is bounded too, not just a single IP
	// hammering the endpoint. Both limiters apply independently. Also
	// reused for the password-reset routes below -- brute-forcing a reset
	// token/request is the same threat class as brute-forcing a password.
	loginAttempts *middleware.KeyedLimiter
}

func NewAuthHandlers(pool *pgxpool.Pool, auth *service.AuthService, ldap *service.LDAPAuthService, saml *service.SAMLAuthService, passwordReset *service.PasswordResetService) *AuthHandlers {
	return &AuthHandlers{
		auth: auth, ldap: ldap, saml: saml, passwordReset: passwordReset,
		loginAttempts: middleware.NewKeyedLimiter(pool, "login_email", 10, 15*time.Minute),
	}
}

func (h *AuthHandlers) Routes(r chi.Router) {
	r.Post("/login", h.loginLocal)
	r.Post("/login/ldap", h.loginLDAP)
	r.Post("/refresh", h.refresh)
	r.Get("/saml/metadata", h.samlMetadata)
	r.Get("/saml/login", h.samlLogin)
	r.Post("/saml/acs", h.samlACS)
	r.Post("/password-reset/request", h.passwordResetRequest)
	r.Post("/password-reset/confirm", h.passwordResetConfirm)
}

// resolveTenant fetches the single tenant every deployment has. A nil
// tenant here means the seed migration (0013_seed_default_admin.up.sql)
// never ran or was rolled back -- an operational misconfiguration, not a
// per-request condition, but every handler still has to handle it since
// nothing guarantees the DB is in the expected state.
func (h *AuthHandlers) resolveTenant(w http.ResponseWriter, r *http.Request) (*domain.Tenant, bool) {
	tenant, err := h.auth.ResolveDefaultTenant(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if tenant == nil {
		writeError(w, http.StatusInternalServerError, "no tenant configured -- has the database been migrated?")
		return nil, false
	}
	return tenant, true
}

// normalizeLoginKey lower-cases and trims the submitted email before using
// it as a rate-limiter key, so "User@x.com" and "user@x.com " don't get
// separate attempt budgets -- a trivial bypass otherwise.
func normalizeLoginKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token        string    `json:"token"`
	RefreshToken string    `json:"refreshToken"`
	User         loginUser `json:"user"`
}

type loginUser struct {
	ID                 string  `json:"id"`
	Email              string  `json:"email"`
	Name               string  `json:"name"`
	Phone              *string `json:"phone,omitempty"`
	Role               string  `json:"role"`
	MustChangePassword bool    `json:"mustChangePassword"`
}

func (h *AuthHandlers) loginLocal(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.loginAttempts.Allow(normalizeLoginKey(req.Email)) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts for this account -- try again later")
		return
	}

	user, token, refreshToken, err := h.auth.LoginLocal(r.Context(), tenant.ID, req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if user == nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User: loginUser{
			ID: user.ID.String(), Email: user.Email, Name: user.Name, Phone: user.Phone, Role: user.Role.Name,
			MustChangePassword: user.MustChangePassword,
		},
	})
}

func (h *AuthHandlers) loginLDAP(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.loginAttempts.Allow(normalizeLoginKey(req.Email)) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts for this account -- try again later")
		return
	}

	user, token, refreshToken, err := h.ldap.Login(r.Context(), tenant.ID, req.Email, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User: loginUser{
			ID: user.ID.String(), Email: user.Email, Name: user.Name, Phone: user.Phone, Role: user.Role.Name,
			MustChangePassword: user.MustChangePassword,
		},
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

type refreshResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
}

// refresh exchanges a still-valid refresh token for a new access token,
// rotating the refresh token itself -- see AuthService.Refresh. Mounted
// under the same rate-limited /auth group as login (router.go), since
// brute-forcing a refresh token is the same threat class as brute-forcing a
// password.
func (h *AuthHandlers) refresh(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}

	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refreshToken is required")
		return
	}

	token, newRefreshToken, err := h.auth.Refresh(r.Context(), tenant.ID, req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	writeJSON(w, http.StatusOK, refreshResponse{Token: token, RefreshToken: newRefreshToken})
}

type passwordResetRequestRequest struct {
	Email string `json:"email"`
}

// passwordResetRequest always returns 204, regardless of whether the email
// matched a real, local, active account -- see
// PasswordResetService.RequestReset's doc comment for why the response
// shape must never leak account existence.
func (h *AuthHandlers) passwordResetRequest(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}

	var req passwordResetRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.loginAttempts.Allow(normalizeLoginKey(req.Email)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts for this account -- try again later")
		return
	}

	if err := h.passwordReset.RequestReset(r.Context(), tenant.ID, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type passwordResetConfirmRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

func (h *AuthHandlers) passwordResetConfirm(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}

	var req passwordResetConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.loginAttempts.Allow(normalizeLoginKey(req.Token)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts -- try again later")
		return
	}

	if err := h.passwordReset.ConfirmReset(r.Context(), tenant.ID, req.Token, req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandlers) samlMetadata(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}
	h.saml.ServeMetadata(r.Context(), tenant.ID, w, r)
}

func (h *AuthHandlers) samlLogin(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}
	h.saml.ServeLogin(r.Context(), tenant.ID, w, r)
}

func (h *AuthHandlers) samlACS(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}
	h.saml.ServeACS(r.Context(), tenant.ID, w, r)
}
