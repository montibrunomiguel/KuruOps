package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/service"
)

// RoleHandlers is Settings -> Roles: admin-only CRUD for the named,
// reusable access bundles users and LDAP/SAML group mappings are assigned
// to. See domain.Role's doc comment.
type RoleHandlers struct {
	svc *service.RoleService
}

func NewRoleHandlers(svc *service.RoleService) *RoleHandlers {
	return &RoleHandlers{svc: svc}
}

func (h *RoleHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
}

func (h *RoleHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	roles, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

type saveRoleRequest struct {
	Name           string                `json:"name"`
	IsAdmin        bool                  `json:"isAdmin"`
	ResourceAccess domain.ResourceAccess `json:"resourceAccess"`
	AllowedTags    []string              `json:"allowedTags"`
}

func (req saveRoleRequest) toInput() domain.SaveRoleInput {
	return domain.SaveRoleInput{
		Name: req.Name, IsAdmin: req.IsAdmin,
		ResourceAccess: req.ResourceAccess, AllowedTags: req.AllowedTags,
	}
}

func (h *RoleHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	role, err := h.svc.Create(r.Context(), tenantID, actorID, req.toInput())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, role)
}

func (h *RoleHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var req saveRoleRequest
	id, ok := decodeAndParseID(w, r, "role", &req)
	if !ok {
		return
	}

	role, err := h.svc.Update(r.Context(), tenantID, actorID, id, req.toInput())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, role)
}

func (h *RoleHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid role id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, actorID, id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
