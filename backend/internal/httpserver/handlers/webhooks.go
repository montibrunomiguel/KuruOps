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
}

func (h *WebhookHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
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
}

func (h *WebhookHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())

	var req createWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Source == "" {
		writeError(w, http.StatusBadRequest, "name and source are required")
		return
	}

	result, err := h.svc.Create(r.Context(), tenantID, userID, req.Name, req.Source, req.ExpiresInDays)
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
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid endpoint id")
		return
	}

	// Body is optional here -- a plain "Regenerate" click with no payload
	// falls back to the default 90-day expiry, same as Create.
	var req regenerateWebhookRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	token, err := h.svc.Regenerate(r.Context(), tenantID, id, req.ExpiresInDays)
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

func (h *WebhookHandlers) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid endpoint id")
		return
	}
	if err := h.svc.SetStatus(r.Context(), tenantID, id, status); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
