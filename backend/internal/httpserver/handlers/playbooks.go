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

type PlaybookHandlers struct {
	svc *service.PlaybookService
}

func NewPlaybookHandlers(svc *service.PlaybookService) *PlaybookHandlers {
	return &PlaybookHandlers{svc: svc}
}

func (h *PlaybookHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Get("/match", h.match)
	r.Post("/steps/{stepId}/trigger", h.triggerStepWebhook)
}

func (h *PlaybookHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	playbooks, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, playbooks)
}

func (h *PlaybookHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid playbook id")
		return
	}
	pb, err := h.svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pb == nil {
		writeError(w, http.StatusNotFound, "playbook not found")
		return
	}
	writeJSON(w, http.StatusOK, pb)
}

// match implements the "Related Playbook" auto-suggestion on Alert Detail:
// GET /api/v1/playbooks/match?title=<alert title>. Kept for previewing what
// a not-yet-saved title would match -- the actual per-alert assignment used
// by AlertDetailPage now comes from the alert's own stored playbookId/
// playbookTitle (set once at ingest time), not a live call to this route.
func (h *PlaybookHandlers) match(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	title := r.URL.Query().Get("title")
	if title == "" {
		writeError(w, http.StatusBadRequest, "title query param is required")
		return
	}
	pb, err := h.svc.MatchForAlertTitle(r.Context(), tenantID, title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if pb == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, pb)
}

type savePlaybookStepRequest struct {
	Text                   string `json:"text"`
	WebhookURL             string `json:"webhookUrl,omitempty"`
	WebhookPayloadTemplate string `json:"webhookPayloadTemplate,omitempty"`
}

type savePlaybookRequest struct {
	Title    string `json:"title"`
	Category string `json:"category"`
	// AlertNamePattern/IsDefault are optional and, like every other playbook
	// field, changeable later via update -- empty pattern + isDefault=false
	// means this playbook never auto-assigns to any alert.
	AlertNamePattern string                                             `json:"alertNamePattern"`
	IsDefault        bool                                               `json:"isDefault"`
	Description      string                                             `json:"description"`
	Keywords         []string                                           `json:"keywords"`
	Steps            map[domain.IncidentPhase][]savePlaybookStepRequest `json:"steps"`
}

func toStepInputs(req map[domain.IncidentPhase][]savePlaybookStepRequest) map[domain.IncidentPhase][]domain.SavePlaybookStepInput {
	out := make(map[domain.IncidentPhase][]domain.SavePlaybookStepInput, len(req))
	for phase, steps := range req {
		converted := make([]domain.SavePlaybookStepInput, len(steps))
		for i, step := range steps {
			converted[i] = domain.SavePlaybookStepInput{
				Text:                   step.Text,
				WebhookURL:             step.WebhookURL,
				WebhookPayloadTemplate: step.WebhookPayloadTemplate,
			}
		}
		out[phase] = converted
	}
	return out
}

func (h *PlaybookHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req savePlaybookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	pb, err := h.svc.Create(r.Context(), tenantID, userID, domain.SavePlaybookInput{
		Title:            req.Title,
		Category:         req.Category,
		Description:      req.Description,
		Keywords:         req.Keywords,
		AlertNamePattern: req.AlertNamePattern,
		IsDefault:        req.IsDefault,
		Steps:            toStepInputs(req.Steps),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, pb)
}

func (h *PlaybookHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid playbook id")
		return
	}

	var req savePlaybookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	pb, err := h.svc.Update(r.Context(), tenantID, id, domain.SavePlaybookInput{
		Title:            req.Title,
		Category:         req.Category,
		Description:      req.Description,
		Keywords:         req.Keywords,
		AlertNamePattern: req.AlertNamePattern,
		IsDefault:        req.IsDefault,
		Steps:            toStepInputs(req.Steps),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pb)
}

func (h *PlaybookHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid playbook id")
		return
	}
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type triggerStepWebhookRequest struct {
	AlertID string `json:"alertId"`
}

// triggerStepWebhook fires the real outbound POST configured on a playbook
// step -- see PlaybookService.TriggerStepWebhook. 502 on any service-side
// failure (step/alert not found, or the destination webhook itself
// rejected/timed out) since from the caller's point of view this is always
// "the downstream automation didn't go through", not a client-request
// problem, once the request itself is well-formed.
func (h *PlaybookHandlers) triggerStepWebhook(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	stepID, err := uuid.Parse(chi.URLParam(r, "stepId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid step id")
		return
	}

	var req triggerStepWebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	alertID, err := uuid.Parse(req.AlertID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "alertId is required")
		return
	}

	if err := h.svc.TriggerStepWebhook(r.Context(), tenantID, userID, stepID, alertID); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
