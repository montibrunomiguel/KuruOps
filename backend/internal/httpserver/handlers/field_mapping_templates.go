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

type FieldMappingTemplateHandlers struct {
	svc *service.FieldMappingTemplateService
}

func NewFieldMappingTemplateHandlers(svc *service.FieldMappingTemplateService) *FieldMappingTemplateHandlers {
	return &FieldMappingTemplateHandlers{svc: svc}
}

// Routes is mounted at /api/v1/settings/field-mapping-templates -- admin
// only, same as /api/v1/settings/webhooks (see router.go). Unlike Tags,
// there's no separate public read route: only the Settings -> Webhook
// Endpoints form needs this catalog, and that's already an admin surface.
func (h *FieldMappingTemplateHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
}

func (h *FieldMappingTemplateHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	templates, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

type fieldMappingTemplateRequest struct {
	Name  string                    `json:"name"`
	Rules []domain.FieldMappingRule `json:"rules"`
}

func (h *FieldMappingTemplateHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req fieldMappingTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	template, err := h.svc.Create(r.Context(), tenantID, userID, req.Name, req.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, template)
}

func (h *FieldMappingTemplateHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	var req fieldMappingTemplateRequest
	id, ok := decodeAndParseID(w, r, "template", &req)
	if !ok {
		return
	}

	template, err := h.svc.Update(r.Context(), tenantID, id, req.Name, req.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, template)
}

func (h *FieldMappingTemplateHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid template id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
