package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

type WebhookHandlers struct {
	svc *service.WebhookService
}

func NewWebhookHandlers(svc *service.WebhookService) *WebhookHandlers {
	return &WebhookHandlers{svc: svc}
}

func (h *WebhookHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Post("/{id}/regenerate", h.regenerate)
	r.Post("/{id}/disable", h.disable)
	r.Post("/{id}/enable", h.enable)
	r.Put("/{id}/field-mapping-template", h.setFieldMappingTemplate)
	r.Put("/{id}/group-by-fields", h.setGroupByFields)
}

func (h *WebhookHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	endpoints, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, endpoints)
}

type createWebhookRequest struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	// ExpiresInDays: omitted/null -> service default (90d); 0 or negative ->
	// endpoint never expires. See service.resolveExpiry.
	ExpiresInDays *int `json:"expiresInDays,omitempty"`
	// FieldMappingTemplateID is optional; unlike Name/Source it can be
	// changed later via setFieldMappingTemplate below.
	FieldMappingTemplateID *uuid.UUID `json:"fieldMappingTemplateId,omitempty"`
	// GroupByFields/DedupWindowMinutes are optional and, like
	// FieldMappingTemplateID, changeable later (via setGroupByFields below).
	// Empty/omitted GroupByFields means dedup is off, same as today's
	// behavior. DedupWindowMinutes omitted/<=0 -> service default (30min,
	// see service.resolveDedupWindow).
	GroupByFields      []string `json:"groupByFields,omitempty"`
	DedupWindowMinutes *int     `json:"dedupWindowMinutes,omitempty"`
}

func (h *WebhookHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req createWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Source == "" {
		writeError(w, http.StatusBadRequest, "name and source are required")
		return
	}

	result, err := h.svc.Create(r.Context(), tenantID, userID, req.Name, req.Source, req.ExpiresInDays, req.FieldMappingTemplateID, req.GroupByFields, req.DedupWindowMinutes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Token is included in this response body only -- it is not retrievable
	// again after this request (see service.CreateResult).
	writeJSON(w, http.StatusCreated, map[string]any{
		"endpoint": result.Endpoint,
		"token":    result.Token,
	})
}

type regenerateWebhookRequest struct {
	ExpiresInDays *int `json:"expiresInDays,omitempty"`
}

func (h *WebhookHandlers) regenerate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid endpoint id")
		return
	}

	// Body is optional here -- a plain "Regenerate" click with no payload
	// falls back to the default 90-day expiry, same as Create.
	var req regenerateWebhookRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	token, err := h.svc.Regenerate(r.Context(), tenantID, userID, id, req.ExpiresInDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (h *WebhookHandlers) disable(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "disabled")
}

func (h *WebhookHandlers) enable(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "active")
}

type setFieldMappingTemplateRequest struct {
	// TemplateID null/omitted clears the association -- the endpoint goes
	// back to only the sender's own top-level "metadata" object.
	TemplateID *uuid.UUID `json:"templateId"`
}

func (h *WebhookHandlers) setFieldMappingTemplate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var req setFieldMappingTemplateRequest
	id, ok := decodeAndParseID(w, r, "endpoint", &req)
	if !ok {
		return
	}

	if err := h.svc.SetFieldMappingTemplate(r.Context(), tenantID, userID, id, req.TemplateID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setGroupByFieldsRequest struct {
	// GroupByFields null/omitted/empty turns dedup back off for this
	// endpoint -- same "clears the association" shape as
	// setFieldMappingTemplateRequest.TemplateID.
	GroupByFields      []string `json:"groupByFields"`
	DedupWindowMinutes *int     `json:"dedupWindowMinutes,omitempty"`
}

func (h *WebhookHandlers) setGroupByFields(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var req setGroupByFieldsRequest
	id, ok := decodeAndParseID(w, r, "endpoint", &req)
	if !ok {
		return
	}

	if err := h.svc.SetGroupByFields(r.Context(), tenantID, userID, id, req.GroupByFields, req.DedupWindowMinutes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *WebhookHandlers) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid endpoint id")
		return
	}
	if err := h.svc.SetStatus(r.Context(), tenantID, userID, id, status); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
