package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
)

type AlertHandlers struct {
	svc *service.AlertService
	// incidents is used only by escalate, which is the one place in the
	// codebase that legitimately needs to know about both aggregates --
	// creating an incident from an alert and linking the two together. Kept
	// at this handler seam (same two-service pattern as MCPServerHandlers)
	// rather than adding cross-service coupling into AlertService/IncidentService.
	incidents *service.IncidentService
	ai        *service.AIAnalysisService
	// users resolves the acting user's display name for addComment -- same
	// reasoning as IncidentHandlers.users (see domain.AlertComment.AuthorName).
	users *service.UserService
}

func NewAlertHandlers(svc *service.AlertService, incidents *service.IncidentService, ai *service.AIAnalysisService, users *service.UserService) *AlertHandlers {
	return &AlertHandlers{svc: svc, incidents: incidents, ai: ai, users: users}
}

func (h *AlertHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Get("/{id}", h.get)
	r.Post("/{id}/status", h.changeStatus)
	r.Post("/{id}/close", h.close)
	r.Put("/{id}/tags", h.updateTags)
	r.Put("/{id}/severity", h.overrideSeverity)
	r.Put("/{id}/assignee", h.reassign)
	r.Get("/{id}/alerts", h.listLinkedAlerts)
	r.Put("/{id}/alerts/{otherId}", h.linkAlert)
	r.Delete("/{id}/alerts/{otherId}", h.unlinkAlert)
	r.Post("/{id}/escalate", h.escalate)
	r.Post("/{id}/analyze", h.analyze)
	r.Get("/{id}/comments", h.listComments)
	r.Post("/{id}/comments", h.addComment)
}

func (h *AlertHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	f := repository.ListAlertsFilter{}
	if v := r.URL.Query().Get("severity"); v != "" {
		s := domain.Severity(v)
		f.Severity = &s
	}
	if v := r.URL.Query().Get("status"); v != "" {
		s := domain.AlertStatus(v)
		f.Status = &s
	}
	if v := r.URL.Query().Get("source"); v != "" {
		f.Source = &v
	}
	if v := r.URL.Query().Get("tag"); v != "" {
		f.Tag = &v
	}
	if v := r.URL.Query().Get("correlated"); v != "" {
		b := v == "true"
		f.Correlated = &b
	}
	f.Limit, f.Offset = parsePaging(r)
	f.AllowedTags = middleware.AllowedTags(r.Context())

	alerts, err := h.svc.List(r.Context(), tenantID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (h *AlertHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	alert, err := h.svc.Get(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if alert == nil {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}
	writeJSON(w, http.StatusOK, alert)
}

type changeStatusRequest struct {
	Status domain.AlertStatus `json:"status"`
}

func (h *AlertHandlers) changeStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	var req changeStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.ChangeStatus(r.Context(), tenantID, id, userID, req.Status, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type closeAlertRequest struct {
	Classification domain.Classification `json:"classification"`
	Comment        string                `json:"comment"`
	ImageURL       *string               `json:"imageUrl,omitempty"`
}

func (h *AlertHandlers) close(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	var req closeAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in := domain.CloseAlertInput{
		Classification: req.Classification,
		Comment:        req.Comment,
		ImageURL:       req.ImageURL,
	}
	if err := h.svc.Close(r.Context(), tenantID, id, userID, in, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateTagsRequest struct {
	Tags []string `json:"tags"`
}

func (h *AlertHandlers) updateTags(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	var req updateTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.UpdateTags(r.Context(), tenantID, id, userID, req.Tags, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type overrideSeverityRequest struct {
	Severity domain.Severity `json:"severity"`
}

func (h *AlertHandlers) overrideSeverity(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	var req overrideSeverityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.OverrideSeverity(r.Context(), tenantID, id, userID, req.Severity, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type reassignAlertRequest struct {
	AnalystID *uuid.UUID `json:"analystId"`
}

func (h *AlertHandlers) reassign(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	var req reassignAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.Reassign(r.Context(), tenantID, id, userID, req.AnalystID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AlertHandlers) listLinkedAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	linked, err := h.svc.LinkedAlerts(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, linked)
}

func (h *AlertHandlers) linkAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	otherID, err := uuid.Parse(chi.URLParam(r, "otherId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid linked alert id")
		return
	}

	if err := h.svc.LinkAlert(r.Context(), tenantID, id, otherID, userID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AlertHandlers) unlinkAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	otherID, err := uuid.Parse(chi.URLParam(r, "otherId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid linked alert id")
		return
	}

	if err := h.svc.UnlinkAlert(r.Context(), tenantID, id, otherID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type escalateResponse struct {
	IncidentID uuid.UUID `json:"incidentId"`
}

// escalate creates a new incident from the alert (title/severity/tags
// copied over, priority defaults to p3 since severity alone doesn't map
// cleanly to a response priority), links the alert to it, and marks the
// alert 'escalated' -- the one place that legitimately touches both
// aggregates, see the doc comment on AlertHandlers.incidents.
func (h *AlertHandlers) escalate(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	allowedTags := middleware.AllowedTags(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	alert, err := h.svc.Get(r.Context(), tenantID, id, allowedTags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if alert == nil {
		writeError(w, http.StatusNotFound, "alert not found")
		return
	}

	incident, err := h.incidents.Create(r.Context(), tenantID, userID, domain.CreateIncidentInput{
		Title:    alert.Title,
		Severity: alert.Severity,
		Priority: domain.PriorityP3,
		Tags:     alert.Tags,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.incidents.LinkAlert(r.Context(), tenantID, incident.ID, alert.ID, userID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.svc.ChangeStatus(r.Context(), tenantID, id, userID, domain.AlertStatusEscalated, allowedTags); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, escalateResponse{IncidentID: incident.ID})
}

type analyzeResponse struct {
	Result string `json:"result"`
}

// analyze is synchronous -- an LLM call can take several seconds, so the
// frontend shows a loading state while this blocks, rather than polling a
// background job (see AIAnalysisService's doc comment).
func (h *AlertHandlers) analyze(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	result, err := h.ai.AnalyzeAlert(r.Context(), tenantID, id, userID, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, analyzeResponse{Result: result})
}

func (h *AlertHandlers) listComments(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	comments, err := h.svc.Comments(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, comments)
}

type addAlertCommentRequest struct {
	Body     string  `json:"body"`
	ImageURL *string `json:"imageUrl,omitempty"`
}

func (h *AlertHandlers) addComment(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	var req addAlertCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}

	// The JWT doesn't carry the user's display name -- resolve it here so
	// the comment can denormalize AuthorName (see domain.AlertComment),
	// same reasoning as IncidentHandlers.addComment.
	actor, err := h.users.Get(r.Context(), tenantID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	comment, err := h.svc.AddComment(r.Context(), tenantID, id, userID, actor.Name, req.Body, req.ImageURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}
