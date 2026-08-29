package handlers

import (
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

// parseSince parses the "since" query param shared by /stats, /activity,
// and the Follow-up view -- the Dashboard's time-range filter, an RFC3339
// timestamp computed client-side from a preset (last 24h/7d/30d/90d) or
// omitted entirely for "any time". A malformed value is treated the same
// as absent rather than erroring the whole request -- this is a filter,
// not a required input.
func parseSince(r *http.Request) *time.Time {
	return parseTimeQueryParam(r, "since")
}

// parseUntil is parseSince's upper-bound counterpart -- the Dashboard's
// custom date-range picker sends both when a range (not just a preset "last
// N days") is selected; a preset sends only since.
func parseUntil(r *http.Request) *time.Time {
	return parseTimeQueryParam(r, "until")
}

func parseTimeQueryParam(r *http.Request, name string) *time.Time {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil
	}
	return &t
}

// parseUUIDQueryParam is the same "malformed = absent, not an error" leniency
// as parseSince, for single-value uuid dashboard filters.
func parseUUIDQueryParam(r *http.Request, name string) *uuid.UUID {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil
	}
	return &id
}

// parseUUIDListQueryParam is parseUUIDQueryParam's multi-select
// counterpart, for the assignedAnalystId/commanderId dashboard filters --
// the frontend joins a multi-select's chosen values with a comma (see
// AlertsTabPanel/IncidentsTabPanel), matching the single-param-not-
// repeated-key convention every other dashboard filter here already uses.
// A malformed individual value is dropped, not treated as an error, same
// "this is a filter, not a required input" leniency as the rest of this
// file.
func parseUUIDListQueryParam(r *http.Request, name string) []uuid.UUID {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	var ids []uuid.UUID
	for _, part := range strings.Split(v, ",") {
		if id, err := uuid.Parse(part); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// parseStringListQueryParam splits a comma-separated query param into a
// []T of a named string type (domain.Severity, domain.AlertStatus) --
// blank segments (e.g. a stray trailing comma) are dropped rather than
// producing an empty-string filter value that could never match a row.
func parseStringListQueryParam[T ~string](r *http.Request, name string) []T {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	var out []T
	for _, part := range strings.Split(v, ",") {
		if part != "" {
			out = append(out, T(part))
		}
	}
	return out
}

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
// severity/tag params. Every filter except alertSource is multi-select: the
// frontend sends a comma-separated list of chosen values in one param
// (e.g. "alertSeverity=critical,high") rather than a repeated query key.
func (h *DashboardHandlers) stats(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	f := repository.StatsFilter{}
	f.AlertSeverity = parseStringListQueryParam[domain.Severity](r, "alertSeverity")
	f.AlertStatus = parseStringListQueryParam[domain.AlertStatus](r, "alertStatus")
	if v := q.Get("alertSource"); v != "" {
		f.AlertSource = &v
	}
	f.AlertTag = parseStringListQueryParam[string](r, "alertTag")
	f.AssignedAnalystID = parseUUIDListQueryParam(r, "assignedAnalystId")
	f.IncidentSeverity = parseStringListQueryParam[domain.Severity](r, "incidentSeverity")
	f.IncidentTag = parseStringListQueryParam[string](r, "incidentTag")
	f.CommanderID = parseUUIDListQueryParam(r, "commanderId")
	f.Since = parseSince(r)
	f.Until = parseUntil(r)
	f.AllowedTags = middleware.AllowedTags(r.Context())

	stats, err := h.svc.Stats(r.Context(), tenantID, f)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *DashboardHandlers) activity(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	kind := r.URL.Query().Get("kind")

	events, err := h.svc.Activity(r.Context(), tenantID, limit, kind, middleware.AllowedTags(r.Context()), parseSince(r), parseUntil(r))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (h *DashboardHandlers) followup(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Followup(r.Context(), tenantID, middleware.AllowedTags(r.Context()), parseSince(r), parseUntil(r))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
