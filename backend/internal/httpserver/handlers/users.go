package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

type UserHandlers struct {
	svc *service.UserService
	// auth is used only for RevokeSessions -- deactivating a user or an
	// explicit admin "Revoke sessions" action needs to invalidate that
	// user's refresh tokens, which live behind AuthService, not
	// UserService. Injected here rather than into UserService's
	// constructor, which has far more call sites to update.
	auth *service.AuthService
}

func NewUserHandlers(svc *service.UserService, auth *service.AuthService) *UserHandlers {
	return &UserHandlers{svc: svc, auth: auth}
}

func (h *UserHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}/access", h.updateAccess)
	r.Post("/{id}/deactivate", h.deactivate)
	r.Post("/{id}/activate", h.activate)
	r.Post("/{id}/reset-password", h.resetPassword)
	r.Post("/{id}/revoke-sessions", h.revokeSessions)
	r.Get("/group-mappings", h.listGroupMappings)
	r.Put("/group-mappings/{provider}/{group}", h.saveGroupMapping)
	r.Delete("/group-mappings/{id}", h.deleteGroupMapping)
}

// Directory is mounted separately from Routes (see router.go) -- it's
// deliberately not under the admin-gated /settings/users group, since any
// authenticated user needs it to resolve a name for a userID they can see
// (e.g. domain.Incident.Assignees) or to populate an assignee picker,
// without being handed the full admin user list (role/resourceAccess/email/etc).
func (h *UserHandlers) Directory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	summaries, err := h.svc.ListSummaries(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (h *UserHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	users, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type createUserRequest struct {
	Email  string    `json:"email"`
	Name   string    `json:"name"`
	RoleID uuid.UUID `json:"roleId"`
}

type createUserResponse struct {
	User domain.User `json:"user"`
	// TemporaryPassword is only ever present in this one response -- it is
	// not retrievable again afterward, so the frontend must show it to the
	// admin immediately (see UserService.CreateLocal).
	TemporaryPassword string `json:"temporaryPassword"`
}

func (h *UserHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, tempPassword, err := h.svc.CreateLocal(r.Context(), tenantID, req.Email, req.Name, req.RoleID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, createUserResponse{User: *user, TemporaryPassword: tempPassword})
}

type updateAccessRequest struct {
	RoleID uuid.UUID `json:"roleId"`
}

func (h *UserHandlers) updateAccess(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req updateAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.UpdateAccess(r.Context(), tenantID, id, req.RoleID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *UserHandlers) deactivate(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, false)
}

func (h *UserHandlers) activate(w http.ResponseWriter, r *http.Request) {
	h.setActive(w, r, true)
}

func (h *UserHandlers) setActive(w http.ResponseWriter, r *http.Request, active bool) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.svc.SetActive(r.Context(), tenantID, id, active); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Deactivating a user should also cut off any session they're already
	// holding, not just block future logins -- best-effort: a failure here
	// doesn't roll back the deactivation itself (SetActive already
	// succeeded), it just means the old refresh tokens linger until they'd
	// have expired naturally.
	if !active {
		_ = h.auth.RevokeSessions(r.Context(), tenantID, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeSessions lets an admin end a user's active session(s) without
// deactivating the account -- e.g. after a suspected compromised device.
// The user's current access token still works until its own 15-minute
// expiry; this only stops it from being renewed via POST /auth/refresh.
func (h *UserHandlers) revokeSessions(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.auth.RevokeSessions(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type resetPasswordResponse struct {
	// TemporaryPassword is only ever present in this one response -- same
	// show-once convention as createUserResponse.
	TemporaryPassword string `json:"temporaryPassword"`
}

func (h *UserHandlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	tempPassword, err := h.svc.ResetPassword(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resetPasswordResponse{TemporaryPassword: tempPassword})
}

func (h *UserHandlers) listGroupMappings(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	mappings, err := h.svc.ListGroupMappings(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, mappings)
}

func (h *UserHandlers) saveGroupMapping(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	provider := domain.AuthProvider(chi.URLParam(r, "provider"))
	group := chi.URLParam(r, "group")

	var req updateAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	m, err := h.svc.SaveGroupMapping(r.Context(), tenantID, provider, group, req.RoleID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *UserHandlers) deleteGroupMapping(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mapping id")
		return
	}
	if err := h.svc.DeleteGroupMapping(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
