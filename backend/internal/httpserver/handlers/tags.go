package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

type TagHandlers struct {
	svc *service.TagService
}

func NewTagHandlers(svc *service.TagService) *TagHandlers {
	return &TagHandlers{svc: svc}
}

// Routes is mounted at /api/v1/tags -- read-only, for any authenticated
// user (an analyst needs to see the catalog to pick from it when tagging an
// alert/incident). Create/Delete are admin-only, mounted separately under
// /api/v1/settings/tags -- see router.go.
func (h *TagHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
}

func (h *TagHandlers) SettingsRoutes(r chi.Router) {
	r.Post("/", h.create)
	r.Delete("/{id}", h.delete)
}

func (h *TagHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	tags, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

type createTagRequest struct {
	Name  string  `json:"name"`
	Color *string `json:"color,omitempty"`
}

func (h *TagHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())

	var req createTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tag, err := h.svc.Create(r.Context(), tenantID, userID, req.Name, req.Color)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, tag)
}

func (h *TagHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid tag id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
