package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
)

// AccountHandlers is where an authenticated caller manages their own
// account. Mounted under /api/v1/account -- unlike AuthHandlers (login,
// unauthenticated), these routes need to know who's calling.
type AccountHandlers struct {
	auth      *service.AuthService
	apiTokens *service.UserAPITokenService
}

func NewAccountHandlers(auth *service.AuthService, apiTokens *service.UserAPITokenService) *AccountHandlers {
	return &AccountHandlers{auth: auth, apiTokens: apiTokens}
}

// ChangePasswordPath is registered on the router and passed to
// middleware.RequirePasswordChanged so it can exempt exactly this route
// from the "must change password first" lockout.
const ChangePasswordPath = "/api/v1/account/change-password"

func (h *AccountHandlers) Routes(r chi.Router) {
	r.Post("/change-password", h.changePassword)
	r.Put("/profile", h.updateProfile)
	r.Post("/mfa/enroll", h.enrollMFA)
	r.Put("/mfa", h.confirmMFA)
	r.Delete("/mfa", h.disableMFA)
	r.Get("/api-tokens", h.listAPITokens)
	r.Post("/api-tokens", h.createAPIToken)
	r.Delete("/api-tokens/{id}", h.revokeAPIToken)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// changePassword is the only route reachable while a caller's token has
// MustChangePassword set (see middleware.RequirePasswordChanged) -- it's
// also just the normal "change my password" endpoint for everyone else.
// Re-issues the session token with MustChangePassword cleared so the
// frontend can swap it in immediately, no fresh login required.
func (h *AccountHandlers) changePassword(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	token, err := h.auth.ChangePassword(r.Context(), tenantID, userID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

type updateProfileRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	// Phone is a pointer so a request that omits the field entirely (e.g. a
	// client that only knows about name/email) leaves the stored phone
	// number untouched, rather than silently clearing it -- see
	// AuthService.UpdateProfile. An explicit "phone": "" clears it.
	Phone           *string `json:"phone"`
	CurrentPassword string  `json:"currentPassword"`
}

// updateProfile lets the caller change their own name/email/phone -- see
// AuthService.UpdateProfile for the local-only guard and the
// email-change-requires-password rule (phone carries no such guard).
func (h *AccountHandlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.auth.UpdateProfile(r.Context(), tenantID, userID, req.Name, req.Email, req.Phone, req.CurrentPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// enrollMFA starts (or restarts) TOTP enrollment: generates a fresh secret
// + otpauth:// URI for the frontend to render as a QR code, but doesn't
// persist anything -- see AuthService.GenerateMFAEnrollment's doc comment
// for why. The secret round-trips back through confirmMFA below, proven by
// producing a real code from it.
func (h *AccountHandlers) enrollMFA(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	secret, otpauthURL, err := h.auth.GenerateMFAEnrollment(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauthUrl": otpauthURL})
}

type confirmMFARequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

// confirmMFA activates 2FA: the frontend sends back the secret enrollMFA
// handed it, plus the code the user's authenticator app produced from it --
// see AuthService.ConfirmMFA. Returns a fresh token (mfa_enabled now true)
// the same way changePassword does, for the same reason.
func (h *AccountHandlers) confirmMFA(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req confirmMFARequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	token, err := h.auth.ConfirmMFA(r.Context(), tenantID, userID, req.Secret, req.Code)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

type disableMFARequest struct {
	CurrentPassword string `json:"currentPassword"`
}

// disableMFA turns 2FA back off -- see AuthService.DisableMFA for why it
// requires the current password. Returns a fresh token (mfa_enabled now
// false), same reasoning as confirmMFA's.
func (h *AccountHandlers) disableMFA(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req disableMFARequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	token, err := h.auth.DisableMFA(r.Context(), tenantID, userID, req.CurrentPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (h *AccountHandlers) listAPITokens(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	tokens, err := h.apiTokens.List(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

type createAPITokenRequest struct {
	Name string `json:"name"`
	// ExpiresInDays: omitted/null -> service default (90d); 0 or negative ->
	// token never expires. Same convention as createWebhookRequest.
	ExpiresInDays *int `json:"expiresInDays,omitempty"`
}

func (h *AccountHandlers) createAPIToken(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req createAPITokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	result, err := h.apiTokens.Create(r.Context(), tenantID, userID, req.Name, req.ExpiresInDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Token is included in this response body only -- it is not retrievable
	// again after this request (see service.CreateAPITokenResult).
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     result.Token,
		"plaintext": result.Plaintext,
	})
}

func (h *AccountHandlers) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token id")
		return
	}

	if err := h.apiTokens.Revoke(r.Context(), tenantID, userID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
