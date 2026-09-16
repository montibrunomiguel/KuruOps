package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
	"github.com/kuruops/kuruops/internal/sessioncookie"
)

// AuthHandlers serves every login flow (local, LDAP, SAML) that converges
// on service.AuthService issuing a session JWT. Mounted unauthenticated at
// /auth -- see httpserver.NewRouter. KuruOps is single-instance software
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

	// secureCookies is the Secure attribute for the refresh cookie,
	// derived once from APP_BASE_URL's scheme -- see secureCookies().
	secureCookies bool
}

func NewAuthHandlers(ctx context.Context, pool *pgxpool.Pool, auth *service.AuthService, ldap *service.LDAPAuthService, saml *service.SAMLAuthService, passwordReset *service.PasswordResetService, appBaseURL string) *AuthHandlers {
	return &AuthHandlers{
		auth: auth, ldap: ldap, saml: saml, passwordReset: passwordReset,
		loginAttempts: middleware.NewKeyedLimiter(ctx, pool, "login_email", 10, 15*time.Minute),
		secureCookies: sessioncookie.Secure(appBaseURL),
	}
}

func (h *AuthHandlers) Routes(r chi.Router) {
	r.Post("/login", h.loginLocal)
	r.Post("/login/ldap", h.loginLDAP)
	r.Post("/refresh", h.refresh)
	r.Post("/logout", h.logout)
	r.Get("/saml/metadata", h.samlMetadata)
	r.Get("/saml/login", h.samlLogin)
	r.Post("/saml/acs", h.samlACS)
	r.Post("/password-reset/request", h.passwordResetRequest)
	r.Post("/password-reset/confirm", h.passwordResetConfirm)
	r.Post("/mfa/verify", h.mfaVerify)
}

// resolveTenant fetches the single tenant every deployment has. A nil
// tenant here means the seed migration (db/migrations/0002_seed_default_admin.up.sql)
// never ran or was rolled back -- an operational misconfiguration, not a
// per-request condition, but every handler still has to handle it since
// nothing guarantees the DB is in the expected state.
func (h *AuthHandlers) resolveTenant(w http.ResponseWriter, r *http.Request) (*domain.Tenant, bool) {
	tenant, err := h.auth.ResolveDefaultTenant(r.Context())
	if err != nil {
		writeInternalError(w, r, err)
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

// loginResponse deliberately carries no refresh token. It is delivered as
// an HttpOnly cookie instead (see sessioncookie.Set), which is only worth
// anything if the value never also appears somewhere script on the page can
// read it -- a response body it can read is exactly that.
type loginResponse struct {
	Token string    `json:"token"`
	User  loginUser `json:"user"`
}

type loginUser struct {
	ID                 string  `json:"id"`
	Email              string  `json:"email"`
	Name               string  `json:"name"`
	Phone              *string `json:"phone,omitempty"`
	Role               string  `json:"role"`
	MustChangePassword bool    `json:"mustChangePassword"`
}

// mfaRequiredResponse is what loginLocal returns instead of loginResponse
// when the account has TOTP enrolled -- the password was correct, but no
// session exists yet. The frontend's login form must show a second,
// code-entry step and call mfaVerify with pendingToken before it has a
// usable session.
type mfaRequiredResponse struct {
	MFARequired  bool   `json:"mfaRequired"`
	PendingToken string `json:"pendingToken"`
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

	user, token, refreshToken, pendingToken, err := h.auth.LoginLocal(r.Context(), tenant.ID, req.Email, req.Password)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if pendingToken != "" {
		writeJSON(w, http.StatusOK, mfaRequiredResponse{MFARequired: true, PendingToken: pendingToken})
		return
	}
	if user == nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	sessioncookie.Set(w, refreshToken, h.secureCookies)
	writeJSON(w, http.StatusOK, loginResponse{
		Token: token,
		User: loginUser{
			ID: user.ID.String(), Email: user.Email, Name: user.Name, Phone: user.Phone, Role: user.Role.Name,
			MustChangePassword: user.MustChangePassword,
		},
	})
}

type mfaVerifyRequest struct {
	PendingToken string `json:"pendingToken"`
	Code         string `json:"code"`
}

// mfaVerify is the second step of a login for a TOTP-enrolled account --
// same opaque-failure discipline as loginLocal itself (an unknown/expired
// pendingToken and a wrong code both read as a plain 401, see
// AuthService.VerifyMFA's doc comment), and rate-limited by pendingToken
// the same way passwordResetConfirm is keyed by its reset token, since
// brute-forcing a 6-digit code is the same threat class.
func (h *AuthHandlers) mfaVerify(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}

	var req mfaVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !h.loginAttempts.Allow(normalizeLoginKey(req.PendingToken)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts -- try again later")
		return
	}

	user, token, refreshToken, err := h.auth.VerifyMFA(r.Context(), tenant.ID, req.PendingToken, req.Code)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if user == nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}

	sessioncookie.Set(w, refreshToken, h.secureCookies)
	writeJSON(w, http.StatusOK, loginResponse{
		Token: token,
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

	sessioncookie.Set(w, refreshToken, h.secureCookies)
	writeJSON(w, http.StatusOK, loginResponse{
		Token: token,
		User: loginUser{
			ID: user.ID.String(), Email: user.Email, Name: user.Name, Phone: user.Phone, Role: user.Role.Name,
			MustChangePassword: user.MustChangePassword,
		},
	})
}

// refreshResponse carries the new access token and the identity it belongs
// to; the rotated refresh token goes back as a Set-Cookie, same reasoning
// as loginResponse.
//
// User is here because the access token lives in memory only: a page load
// has the refresh cookie and nothing else, so this response has to be able
// to rebuild a session on its own. It is also what makes the SAML flow work
// without ever putting a token in a URL or a readable body -- see
// SAMLAuthService.ServeACS.
type refreshResponse struct {
	Token string    `json:"token"`
	User  loginUser `json:"user"`
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

	presented := sessioncookie.Read(r)
	if presented == "" {
		writeError(w, http.StatusUnauthorized, "no refresh token")
		return
	}

	user, token, newRefreshToken, err := h.auth.Refresh(r.Context(), tenant.ID, presented)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if token == "" {
		// Clear the cookie the caller sent: it is expired or revoked, so
		// leaving it in the browser only produces a 401 on every future
		// refresh until it ages out on its own.
		sessioncookie.Clear(w, h.secureCookies)
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}

	sessioncookie.Set(w, newRefreshToken, h.secureCookies)
	writeJSON(w, http.StatusOK, refreshResponse{
		Token: token,
		User: loginUser{
			ID: user.ID.String(), Email: user.Email, Name: user.Name, Phone: user.Phone, Role: user.Role.Name,
			MustChangePassword: user.MustChangePassword,
		},
	})
}

// logout revokes the refresh token the caller presents, so it can't be used
// to mint further access tokens after this point -- see AuthService.Logout.
// Unauthenticated like the rest of this handler group (no access token is
// required, only the refresh token itself), since the whole point is to let
// a client whose access token already expired still end its session
// cleanly. Always returns 204 regardless of whether the token was known,
// already revoked, or omitted -- same "don't let the response distinguish
// states" discipline AuthService.Logout documents, so this endpoint can't
// be used to probe whether a given refresh token is currently valid.
func (h *AuthHandlers) logout(w http.ResponseWriter, r *http.Request) {
	tenant, ok := h.resolveTenant(w, r)
	if !ok {
		return
	}

	// Clear the cookie regardless of what the revoke below does. Getting
	// the user's own browser out of a logged-in state is the half that must
	// not depend on anything else succeeding.
	sessioncookie.Clear(w, h.secureCookies)

	if err := h.auth.Logout(r.Context(), tenant.ID, sessioncookie.Read(r)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		writeInternalError(w, r, err)
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
