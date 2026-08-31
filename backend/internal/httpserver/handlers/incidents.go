package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/httpserver/middleware"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/service"
)

type IncidentHandlers struct {
	svc *service.IncidentService
	// users resolves the acting user's display name for AddComment -- see
	// domain.IncidentComment.AuthorName and UserService.Get's doc comment.
	users      *service.UserService
	ai         *service.AIAnalysisService
	postmortem *service.PostmortemService
	report     *service.IncidentReportService
	// mcpTools backs the AnalysisChat's inline tool-call approve/reject --
	// see AlertHandlers.mcpTools's doc comment, same reasoning.
	mcpTools *service.MCPToolService
}

func NewIncidentHandlers(svc *service.IncidentService, users *service.UserService, ai *service.AIAnalysisService, postmortem *service.PostmortemService, report *service.IncidentReportService, mcpTools *service.MCPToolService) *IncidentHandlers {
	return &IncidentHandlers{svc: svc, users: users, ai: ai, postmortem: postmortem, report: report, mcpTools: mcpTools}
}

func (h *IncidentHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Post("/bulk/phase", h.bulkChangePhase)
	r.Post("/{id}/phase", h.changePhase)
	r.Post("/{id}/close", h.close)
	r.Post("/{id}/severity-priority", h.setSeverityPriority)
	r.Put("/{id}/description", h.updateDescription)
	r.Put("/{id}/tags", h.updateTags)
	r.Put("/{id}/assignees", h.setAssignees)
	r.Put("/{id}/roles/{role}", h.setRole)
	r.Get("/{id}/status-history", h.statusHistory)
	r.Post("/{id}/status-history/{phase}/correct", h.correctPhaseTimestamp)
	r.Get("/{id}/timeline", h.timeline)
	r.Get("/{id}/comments", h.listComments)
	r.Post("/{id}/comments", h.addComment)
	r.Get("/{id}/iocs", h.listIOCs)
	r.Post("/{id}/iocs", h.addIOC)
	r.Get("/{id}/alerts", h.linkedAlerts)
	r.Put("/{id}/alerts/{alertId}", h.linkAlert)
	r.Delete("/{id}/alerts/{alertId}", h.unlinkAlert)
	r.Post("/{id}/analyze", h.analyze)
	r.Get("/{id}/analyze/messages", h.getAnalysisChat)
	r.Post("/{id}/analyze/messages", h.continueAnalysisChat)
	r.Post("/{id}/analyze/tool-calls/{callId}/approve", h.approveAnalysisToolCall)
	r.Post("/{id}/analyze/tool-calls/{callId}/reject", h.rejectAnalysisToolCall)
	r.Get("/{id}/postmortem", h.postmortemDoc)
	r.Get("/{id}/report.pdf", h.reportPDF)
}

func (h *IncidentHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}

	f := repository.ListIncidentsFilter{}
	if v := r.URL.Query().Get("severity"); v != "" {
		s := domain.Severity(v)
		f.Severity = &s
	}
	if v := r.URL.Query().Get("priority"); v != "" {
		p := domain.IncidentPriority(v)
		f.Priority = &p
	}
	if v := r.URL.Query().Get("phase"); v != "" {
		p := domain.IncidentPhase(v)
		f.Phase = &p
	}
	if v := r.URL.Query().Get("tag"); v != "" {
		f.Tag = &v
	}
	if v := r.URL.Query().Get("sla"); v == "breached" || v == "ok" {
		b := v == "breached"
		f.SLABreached = &b
	}
	// Trimmed, not just non-empty -- see alerts.go's identical guard for why.
	if v := strings.TrimSpace(r.URL.Query().Get("q")); v != "" {
		f.Q = &v
	}
	f.OpenedSince = parseSince(r)
	f.OpenedUntil = parseUntil(r)
	f.CommanderID = parseUUIDQueryParam(r, "commanderId")
	f.Limit, f.Offset = parsePaging(r)
	f.AllowedTags = middleware.AllowedTags(r.Context())

	incidents, err := h.svc.List(r.Context(), tenantID, f)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}

	total, err := h.svc.Count(r.Context(), tenantID, f)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, incidents)
}

type createIncidentRequest struct {
	Title       string                  `json:"title"`
	Description string                  `json:"description"`
	Severity    domain.Severity         `json:"severity"`
	Priority    domain.IncidentPriority `json:"priority"`
	AssigneeIDs []uuid.UUID             `json:"assigneeIds"`
	Tags        []string                `json:"tags"`
}

func (h *IncidentHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req createIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	inc, err := h.svc.Create(r.Context(), tenantID, userID, domain.CreateIncidentInput{
		Title:       req.Title,
		Description: req.Description,
		Severity:    req.Severity,
		Priority:    req.Priority,
		AssigneeIDs: req.AssigneeIDs,
		Tags:        req.Tags,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, inc)
}

func (h *IncidentHandlers) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	inc, err := h.svc.Get(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if inc == nil {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

type changePhaseRequest struct {
	Phase domain.IncidentPhase `json:"phase"`
}

func (h *IncidentHandlers) changePhase(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req changePhaseRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}

	if err := h.svc.ChangePhase(r.Context(), tenantID, id, userID, req.Phase, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type bulkChangePhaseRequest struct {
	IDs   []uuid.UUID          `json:"ids"`
	Phase domain.IncidentPhase `json:"phase"`
}

// bulkChangePhase applies one phase change across many incidents (the
// IncidentsListPage row-selection toolbar's Apply button). post_incident is
// rejected up front by IncidentService.BulkChangePhase -- bulk-close was
// explicitly descoped, closing still requires the single-item Close flow.
// A per-incident failure doesn't fail the whole request -- see
// bulkChangeStatus's doc comment, same reasoning.
func (h *IncidentHandlers) bulkChangePhase(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())

	var req bulkChangePhaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	if len(req.IDs) > maxBulkIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d ids per request", maxBulkIDs))
		return
	}

	results, err := h.svc.BulkChangePhase(r.Context(), tenantID, userID, req.IDs, req.Phase, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *IncidentHandlers) close(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	if err := h.svc.Close(r.Context(), tenantID, id, userID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setSeverityPriorityRequest struct {
	Severity domain.Severity         `json:"severity"`
	Priority domain.IncidentPriority `json:"priority"`
}

func (h *IncidentHandlers) setSeverityPriority(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req setSeverityPriorityRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}

	if err := h.svc.SetSeverityAndPriority(r.Context(), tenantID, id, userID, req.Severity, req.Priority, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateDescriptionRequest struct {
	Description string `json:"description"`
}

func (h *IncidentHandlers) updateDescription(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req updateDescriptionRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}

	if err := h.svc.UpdateDescription(r.Context(), tenantID, id, userID, req.Description, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateIncidentTagsRequest struct {
	Tags []string `json:"tags"`
}

func (h *IncidentHandlers) updateTags(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req updateIncidentTagsRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}

	if err := h.svc.UpdateTags(r.Context(), tenantID, id, userID, req.Tags, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setAssigneesRequest struct {
	AssigneeIDs []uuid.UUID `json:"assigneeIds"`
}

func (h *IncidentHandlers) setAssignees(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req setAssigneesRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}

	if err := h.svc.SetAssignees(r.Context(), tenantID, id, userID, req.AssigneeIDs, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setRoleRequest struct {
	UserIDs []uuid.UUID `json:"userIds"`
}

// setRole is Settings-free: role comes from the URL path (one of
// domain.IncidentRoles), not the request body, so a malformed/unknown role
// is a 400 from domain.IncidentRole.Valid() inside IncidentService.SetRole
// rather than a routing 404 -- keeps the error message specific ("unknown
// incident role") instead of a generic not-found.
func (h *IncidentHandlers) setRole(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	actorID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	role := domain.IncidentRole(chi.URLParam(r, "role"))

	var req setRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.SetRole(r.Context(), tenantID, id, actorID, role, req.UserIDs, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IncidentHandlers) statusHistory(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	entries, found, err := h.svc.StatusHistory(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

type correctPhaseTimestampRequest struct {
	EnteredAt time.Time `json:"enteredAt"`
	Reason    string    `json:"reason"`
}

func (h *IncidentHandlers) correctPhaseTimestamp(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	phase := domain.IncidentPhase(chi.URLParam(r, "phase"))

	var req correctPhaseTimestampRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.CorrectPhaseTimestamp(r.Context(), tenantID, id, userID, phase, req.EnteredAt, req.Reason, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IncidentHandlers) timeline(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	events, found, err := h.svc.Timeline(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *IncidentHandlers) listComments(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	comments, found, err := h.svc.Comments(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	writeJSON(w, http.StatusOK, comments)
}

type addCommentRequest struct {
	Body          string  `json:"body"`
	AttachmentURL *string `json:"attachmentUrl,omitempty"`
}

func (h *IncidentHandlers) addComment(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req addCommentRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}
	if req.Body == "" {
		writeError(w, http.StatusBadRequest, "body is required")
		return
	}

	// The JWT doesn't carry the user's display name (see authn.Claims), only
	// their id -- resolve it here so the comment can denormalize AuthorName
	// (see domain.IncidentComment) without a new user-lookup route.
	actor, err := h.users.Get(r.Context(), tenantID, userID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	comment, err := h.svc.AddComment(r.Context(), tenantID, id, userID, actor.Name, req.Body, req.AttachmentURL, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (h *IncidentHandlers) listIOCs(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	iocs, found, err := h.svc.IOCs(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	writeJSON(w, http.StatusOK, iocs)
}

type addIOCRequest struct {
	Type         domain.IOCType `json:"type"`
	Value        string         `json:"value"`
	Description  string         `json:"description"`
	IdentifiedAt time.Time      `json:"identifiedAt"`
}

// addIOC mirrors addComment's actor-name-resolution shape -- see that
// handler's doc comment for why it looks up the user instead of trusting
// something carried in the JWT.
func (h *IncidentHandlers) addIOC(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req addIOCRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}

	actor, err := h.users.Get(r.Context(), tenantID, userID)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}

	ioc, err := h.svc.AddIOC(r.Context(), tenantID, id, userID, actor.Name, req.Type, req.Value, req.Description, req.IdentifiedAt, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ioc)
}

func (h *IncidentHandlers) linkedAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	alerts, found, err := h.svc.LinkedAlerts(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (h *IncidentHandlers) linkAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	alertID, err := uuid.Parse(chi.URLParam(r, "alertId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	if err := h.svc.LinkAlert(r.Context(), tenantID, id, alertID, userID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IncidentHandlers) unlinkAlert(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	alertID, err := uuid.Parse(chi.URLParam(r, "alertId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert id")
		return
	}

	if err := h.svc.UnlinkAlert(r.Context(), tenantID, id, alertID, userID, middleware.AllowedTags(r.Context())); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// analyze kicks off analysis in the background and returns immediately --
// see AlertHandlers.analyze's doc comment, same design.
func (h *IncidentHandlers) analyze(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	err = h.ai.StartIncidentAnalysis(r.Context(), tenantID, id, &userID, middleware.AllowedTags(r.Context()))
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

// getAnalysisChat/continueAnalysisChat/approveAnalysisToolCall/
// rejectAnalysisToolCall are IncidentHandlers' counterparts to
// AlertHandlers' identically-named methods -- see those doc comments.
func (h *IncidentHandlers) getAnalysisChat(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}
	transcript, err := h.ai.GetIncidentTranscript(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, transcript)
}

func (h *IncidentHandlers) continueAnalysisChat(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	var req continueMessageRequest
	id, ok := decodeAndParseID(w, r, "incident", &req)
	if !ok {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	err := h.ai.ContinueIncidentAnalysis(r.Context(), tenantID, id, userID, middleware.AllowedTags(r.Context()), req.Text)
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

func (h *IncidentHandlers) approveAnalysisToolCall(w http.ResponseWriter, r *http.Request) {
	resolveAnalysisToolCall(w, r, h.mcpTools, "incident", true, h.incidentVisible(r))
}

func (h *IncidentHandlers) rejectAnalysisToolCall(w http.ResponseWriter, r *http.Request) {
	resolveAnalysisToolCall(w, r, h.mcpTools, "incident", false, h.incidentVisible(r))
}

// incidentVisible is the visibility gate resolveAnalysisToolCall applies
// before resolving a tool call -- see its doc comment for why.
func (h *IncidentHandlers) incidentVisible(r *http.Request) func(uuid.UUID) (bool, error) {
	return func(id uuid.UUID) (bool, error) {
		tenantID, _ := middleware.TenantID(r.Context())
		inc, err := h.svc.Get(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
		return inc != nil, err
	}
}

// postmortemDoc streams a generated Markdown postmortem for the incident --
// same download-response shape as AuditExportHandlers.exportCEF (forced
// attachment, no JSON envelope). See PostmortemService.Generate for what
// the document contains.
func (h *IncidentHandlers) postmortemDoc(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	doc, found, err := h.postmortem.Generate(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}

	filename := fmt.Sprintf("postmortem-%s.md", id)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(doc))
}
