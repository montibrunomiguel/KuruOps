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
}

func (h *PlaybookHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
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
	tenantID, _ := middleware.TenantID(r.Context())
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
// GET /api/v1/playbooks/match?title=<alert title>
func (h *PlaybookHandlers) match(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
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

type savePlaybookRequest struct {
	Title       string                            `json:"title"`
	Category    string                            `json:"category"`
	Description string                            `json:"description"`
	Keywords    []string                          `json:"keywords"`
	Steps       map[domain.IncidentPhase][]string `json:"steps"`
}

func (h *PlaybookHandlers) create(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
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
		Title:       req.Title,
		Category:    req.Category,
		Description: req.Description,
		Keywords:    req.Keywords,
		Steps:       req.Steps,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, pb)
}

func (h *PlaybookHandlers) update(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
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
		Title:       req.Title,
		Category:    req.Category,
		Description: req.Description,
		Keywords:    req.Keywords,
		Steps:       req.Steps,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pb)
}

func (h *PlaybookHandlers) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, _ := middleware.TenantID(r.Context())
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
