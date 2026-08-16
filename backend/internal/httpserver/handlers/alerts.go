package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
)

type AlertHandlers struct {
	svc *service.AlertService
	ai  *service.AIAnalysisService
	// mcpTools backs the AnalysisChat's inline tool-call approve/reject --
	// same underlying service Settings -> MCP Servers' pending-approvals
	// panel already calls, just also reachable from here so an analyst
	// without admin access can resolve a call from inside the chat itself.
	mcpTools *service.MCPToolService
	// users resolves the acting user's display name for addComment -- same
	// reasoning as IncidentHandlers.users (see domain.AlertComment.AuthorName).
	users *service.UserService
}

func NewAlertHandlers(svc *service.AlertService, ai *service.AIAnalysisService, mcpTools *service.MCPToolService, users *service.UserService) *AlertHandlers {
	return &AlertHandlers{svc: svc, ai: ai, mcpTools: mcpTools, users: users}
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
	r.Get("/{id}/analyze/messages", h.getAnalysisChat)
	r.Post("/{id}/analyze/messages", h.continueAnalysisChat)
	r.Post("/{id}/analyze/tool-calls/{callId}/approve", h.approveAnalysisToolCall)
	r.Post("/{id}/analyze/tool-calls/{callId}/reject", h.rejectAnalysisToolCall)
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
	f.ReceivedSince = parseSince(r)
	f.ReceivedUntil = parseUntil(r)
	f.Limit, f.Offset = parsePaging(r)
	f.AllowedTags = middleware.AllowedTags(r.Context())

	alerts, err := h.svc.List(r.Context(), tenantID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.svc.Count(r.Context(), tenantID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
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
	AttachmentURL  *string               `json:"attachmentUrl,omitempty"`
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
		AttachmentURL:  req.AttachmentURL,
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

// escalate delegates to AlertService.Escalate -- see its doc comment for
// what actually happens (creates+links an incident, marks the alert
// escalated, fires the next manual-escalation chain step).
func (h *AlertHandlers) escalate(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	allowedTags := middleware.AllowedTags(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	incident, err := h.svc.Escalate(r.Context(), tenantID, userID, id, allowedTags)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, escalateResponse{IncidentID: incident.ID})
}

type analyzeStartedResponse struct {
	Status string `json:"status"`
}

// analyze kicks off analysis in the background and returns immediately --
// the eventual result (or failure) shows up on the alert itself (see
// domain.Alert's LatestAnalysis* fields) once a live-update event tells a
// connected AlertDetailPage to reload (see AIAnalysisService's doc
// comment). 409 if one's already running/paused for this alert.
func (h *AlertHandlers) analyze(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	err = h.ai.StartAlertAnalysis(r.Context(), tenantID, id, &userID, middleware.AllowedTags(r.Context()))
	if err != nil {
		if errors.Is(err, service.ErrAnalysisInProgress) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, analyzeStartedResponse{Status: "running"})
}

// continueMessageRequest is the body POST .../analyze/messages takes on
// both alerts and incidents -- kept here (not duplicated in incidents.go),
// same sharing as analyzeStartedResponse above.
type continueMessageRequest struct {
	Text string `json:"text"`
}

// getAnalysisChat backs AnalysisChat's initial load and its
// refetch-on-SSE-event -- see AIAnalysisService.GetAlertTranscript.
func (h *AlertHandlers) getAnalysisChat(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}
	transcript, err := h.ai.GetAlertTranscript(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, transcript)
}

// continueAnalysisChat is what the chat's message box posts to -- kicks off
// the next turn in the background and returns immediately, same 202 shape
// as analyze (see AIAnalysisService.ContinueAlertAnalysis's doc comment for
// why the analyst's own message is already persisted by the time this
// returns, even though the LLM's reply isn't yet).
func (h *AlertHandlers) continueAnalysisChat(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	var req continueMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	err = h.ai.ContinueAlertAnalysis(r.Context(), tenantID, id, userID, middleware.AllowedTags(r.Context()), req.Text)
	if err != nil {
		if errors.Is(err, service.ErrAnalysisInProgress) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, analyzeStartedResponse{Status: "running"})
}

// approveAnalysisToolCall/rejectAnalysisToolCall let the chat itself resolve
// a paused tool-call approval, without needing admin access to Settings ->
// MCP Servers -- same underlying MCPToolService calls that panel's
// PendingApprovalRow already makes (see mcp_servers.go), gated here by
// confirming the call actually belongs to this alert before touching it.
func (h *AlertHandlers) approveAnalysisToolCall(w http.ResponseWriter, r *http.Request) {
	resolveAnalysisToolCall(w, r, h.mcpTools, "alert", true)
}

func (h *AlertHandlers) rejectAnalysisToolCall(w http.ResponseWriter, r *http.Request) {
	resolveAnalysisToolCall(w, r, h.mcpTools, "alert", false)
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
	Body          string  `json:"body"`
	AttachmentURL *string `json:"attachmentUrl,omitempty"`
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

	comment, err := h.svc.AddComment(r.Context(), tenantID, id, userID, actor.Name, req.Body, req.AttachmentURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}
