package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
)

type DashboardHandlers struct {
	svc *service.DashboardService
}

func NewDashboardHandlers(svc *service.DashboardService) *DashboardHandlers {
	return &DashboardHandlers{svc: svc}
}

func (h *DashboardHandlers) Routes(r chi.Router) {
	r.Get("/stats", h.stats)
	r.Get("/activity", h.activity)
}

// FollowupRoutes is mounted separately from Routes -- unlike /stats, the
// Follow-up view is gated by ResourceCapabilityFollowup, independently of
// the general "alerts"/"incidents" capabilities (see router.go).
func (h *DashboardHandlers) FollowupRoutes(r chi.Router) {
	r.Get("/", h.followup)
}

// stats parses two independent filter dimensions off the query string --
// alertSeverity/alertStatus/alertSource/alertTag for the Alerts tab's
// filter bar, incidentSeverity/incidentTag for the Incidents tab's -- see
// repository.StatsFilter for why they're independent rather than shared
// severity/tag params.
func (h *DashboardHandlers) stats(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	q := r.URL.Query()
	f := repository.StatsFilter{}
	if v := q.Get("alertSeverity"); v != "" {
		s := domain.Severity(v)
		f.AlertSeverity = &s
	}
	if v := q.Get("alertStatus"); v != "" {
		s := domain.AlertStatus(v)
		f.AlertStatus = &s
	}
	if v := q.Get("alertSource"); v != "" {
		f.AlertSource = &v
	}
	if v := q.Get("alertTag"); v != "" {
		f.AlertTag = &v
	}
	if v := q.Get("incidentSeverity"); v != "" {
		s := domain.Severity(v)
		f.IncidentSeverity = &s
	}
	if v := q.Get("incidentTag"); v != "" {
		f.IncidentTag = &v
	}
	f.AllowedTags = middleware.AllowedTags(r.Context())

	stats, err := h.svc.Stats(r.Context(), tenantID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *DashboardHandlers) activity(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	kind := r.URL.Query().Get("kind")

	events, err := h.svc.Activity(r.Context(), tenantID, limit, kind, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *DashboardHandlers) followup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}
	view, err := h.svc.Followup(r.Context(), tenantID, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}
