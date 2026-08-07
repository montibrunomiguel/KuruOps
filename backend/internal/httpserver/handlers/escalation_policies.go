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

// EscalationPolicyHandlers is Settings -> On-Call Escalation: admin-only.
type EscalationPolicyHandlers struct {
	svc *service.EscalationPolicyService
}

func NewEscalationPolicyHandlers(svc *service.EscalationPolicyService) *EscalationPolicyHandlers {
	return &EscalationPolicyHandlers{svc: svc}
}

func (h *EscalationPolicyHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Put("/", h.save)
	r.Delete("/{id}", h.delete)
}

func (h *EscalationPolicyHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	policies, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policies)
}

type saveEscalationPolicyRequest struct {
	Severity                   domain.Severity              `json:"severity"`
	UnacknowledgedAfterMinutes int                          `json:"unacknowledgedAfterMinutes"`
	ChannelType                domain.EscalationChannelType `json:"channelType"`
	// Destination is plaintext; "" on update means keep the existing one --
	// see EscalationPolicyService.Save.
	Destination string `json:"destination"`
}

func (h *EscalationPolicyHandlers) save(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())

	var req saveEscalationPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	policy, err := h.svc.Save(r.Context(), tenantID, req.Severity, req.UnacknowledgedAfterMinutes, req.ChannelType, req.Destination)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (h *EscalationPolicyHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid policy id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
