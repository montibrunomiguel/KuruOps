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

// EscalationPolicyHandlers is Settings -> Escala de Acionamento: admin-only.
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
	r.Post("/test", h.test)
}

func (h *EscalationPolicyHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	policies, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policies)
}

type saveEscalationStepRequest struct {
	ScheduleID   uuid.UUID                    `json:"scheduleId"`
	DelayMinutes int                          `json:"delayMinutes"`
	ChannelType  domain.EscalationChannelType `json:"channelType"`
	// Destination is plaintext; "" for a position that already had a step
	// saved there means keep the existing one -- see EscalationPolicyService.Save.
	Destination string `json:"destination"`
	// WebhookPayloadTemplate only applies when ChannelType is webhook; ""
	// means send the default fixed payload shape.
	WebhookPayloadTemplate string `json:"webhookPayloadTemplate"`
}

type saveEscalationPolicyRequest struct {
	Severity domain.Severity             `json:"severity"`
	Steps    []saveEscalationStepRequest `json:"steps"`
}

func (h *EscalationPolicyHandlers) save(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, ok := middleware.UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing user context")
		return
	}

	var req saveEscalationPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	steps := make([]domain.SaveEscalationStepInput, len(req.Steps))
	for i, st := range req.Steps {
		steps[i] = domain.SaveEscalationStepInput{
			ScheduleID: st.ScheduleID, DelayMinutes: st.DelayMinutes,
			ChannelType: st.ChannelType, Destination: st.Destination,
			WebhookPayloadTemplate: st.WebhookPayloadTemplate,
		}
	}

	policy, err := h.svc.Save(r.Context(), tenantID, actorID, domain.SaveEscalationPolicyInput{Severity: req.Severity, Steps: steps})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

type testEscalationPolicyRequest struct {
	Severity domain.Severity `json:"severity"`
	// StepPosition is 0-indexed, matching EscalationStep.Position.
	StepPosition int `json:"stepPosition"`
}

// test sends a real notification through a step of an already-saved chain
// -- lets an admin confirm a destination (and, for webhook, a custom
// payload template with the analyst placeholders) actually works without
// waiting for a real alert to escalate. Mirrors SMTPConfigHandlers' "send
// test email" endpoint.
func (h *EscalationPolicyHandlers) test(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}

	var req testEscalationPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.Test(r.Context(), tenantID, req.Severity, req.StepPosition); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *EscalationPolicyHandlers) delete(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadRequest, "invalid policy id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, actorID, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
