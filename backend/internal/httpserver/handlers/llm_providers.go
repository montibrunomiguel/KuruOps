package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

type LLMProviderHandlers struct {
	svc *service.LLMProviderService
}

func NewLLMProviderHandlers(svc *service.LLMProviderService) *LLMProviderHandlers {
	return &LLMProviderHandlers{svc: svc}
}

func (h *LLMProviderHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Put("/{id}", h.update)
	r.Post("/{id}/default", h.setDefault)
	r.Delete("/{id}", h.delete)
}

func (h *LLMProviderHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	providers, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, providers)
}

type saveLLMProviderRequest struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	BaseURL *string `json:"baseUrl,omitempty"`
	Model   string  `json:"model"`
	// APIKey is write-only: never populated on responses. Empty on update
	// means "keep the existing key" (see LLMProviderService.Update).
	APIKey string `json:"apiKey"`
	// AutoAnalyzeAllAlerts -- see domain.LLMProvider's doc comment. Unlike
	// APIKey, this fully replaces the stored value on every save (same as
	// Name/Kind/BaseURL/Model) -- there's no "omit to keep existing" case.
	AutoAnalyzeAllAlerts bool `json:"autoAnalyzeAllAlerts"`
}

func (h *LLMProviderHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req saveLLMProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Model == "" || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "name, model, and apiKey are required")
		return
	}

	p, err := h.svc.Create(r.Context(), tenantID, userID, service.LLMProviderSaveInput{
		Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL, Model: req.Model, APIKey: req.APIKey,
		AutoAnalyzeAllAlerts: req.AutoAnalyzeAllAlerts,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (h *LLMProviderHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	var req saveLLMProviderRequest
	id, ok := decodeAndParseID(w, r, "provider", &req)
	if !ok {
		return
	}

	p, err := h.svc.Update(r.Context(), tenantID, id, service.LLMProviderSaveInput{
		Name: req.Name, Kind: req.Kind, BaseURL: req.BaseURL, Model: req.Model, APIKey: req.APIKey,
		AutoAnalyzeAllAlerts: req.AutoAnalyzeAllAlerts,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *LLMProviderHandlers) setDefault(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	if err := h.svc.SetDefault(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *LLMProviderHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
