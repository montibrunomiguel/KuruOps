package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

// AccountHandlers is where an authenticated caller manages their own
// account. Mounted under /api/v1/account -- unlike AuthHandlers (login,
// unauthenticated), these routes need to know who's calling.
type AccountHandlers struct {
	auth   *service.AuthService
	tokens *service.PersonalAccessTokenService
}

func NewAccountHandlers(auth *service.AuthService, tokens *service.PersonalAccessTokenService) *AccountHandlers {
	return &AccountHandlers{auth: auth, tokens: tokens}
}

// ChangePasswordPath is registered on the router and passed to
// middleware.RequirePasswordChanged so it can exempt exactly this route
// from the "must change password first" lockout.
const ChangePasswordPath = "/api/v1/account/change-password"

func (h *AccountHandlers) Routes(r chi.Router) {
	r.Post("/change-password", h.changePassword)
	r.Put("/profile", h.updateProfile)
	r.Get("/tokens", h.listTokens)
	r.Post("/tokens", h.createToken)
	r.Delete("/tokens/{id}", h.revokeToken)
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
	tenantID, _ := middleware.TenantID(r.Context())
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
	Name            string `json:"name"`
	Email           string `json:"email"`
	CurrentPassword string `json:"currentPassword"`
}

// updateProfile lets the caller change their own name/email -- see
// AuthService.UpdateProfile for the local-only guard and the
// email-change-requires-password rule.
func (h *AccountHandlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.auth.UpdateProfile(r.Context(), tenantID, userID, req.Name, req.Email, req.CurrentPassword)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (h *AccountHandlers) listTokens(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())

	tokens, err := h.tokens.List(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

type createTokenRequest struct {
	Name string `json:"name"`
	// ExpiresInDays: omitted/null/0/negative -> never expires. Opposite
	// default from webhook tokens -- see PersonalAccessTokenService.Create's
	// doc comment for why.
	ExpiresInDays *int `json:"expiresInDays,omitempty"`
}

// createToken mints a new personal access token for the caller themselves
// -- userID always comes from the authenticated request's own claims,
// never from the request body, so there's no way to mint a token for
// another user through this endpoint.
func (h *AccountHandlers) createToken(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := h.tokens.Create(r.Context(), tenantID, userID, req.Name, req.ExpiresInDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Plaintext is included in this response body only -- it is not
	// retrievable again after this request (see service.CreatePATResult).
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":     result.Token,
		"plaintext": result.Plaintext,
	})
}

func (h *AccountHandlers) revokeToken(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid token id")
		return
	}

	if err := h.tokens.Revoke(r.Context(), tenantID, userID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
