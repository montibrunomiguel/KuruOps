package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
)

const (
	defaultAdminAuditLogLimit = 50
	maxAdminAuditLogLimit     = 200
)

// AdminAuditLogHandlers is Settings -> Data & Audit: admin-only, read-only
// view over admin_audit_events (see domain.AdminAuditEvent). Distinct from
// AuditExportHandlers, which serves the CEF/JSON download of alert/incident
// history -- this is the paginated in-app table of Settings changes.
type AdminAuditLogHandlers struct {
	svc *service.AdminAuditLogService
}

func NewAdminAuditLogHandlers(svc *service.AdminAuditLogService) *AdminAuditLogHandlers {
	return &AdminAuditLogHandlers{svc: svc}
}

func (h *AdminAuditLogHandlers) Routes(r chi.Router) {
	r.Get("/", h.list)
}

// list reads limit/beforeCreatedAt/beforeId query params -- beforeCreatedAt/
// beforeId together are the keyset cursor for the next page (both come from
// the previous response's nextCursor), same "created_at, id" tiebreak
// AdminAuditEventRepository.List uses. Omitting them returns the first
// (newest) page.
func (h *AdminAuditLogHandlers) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}

	limit := defaultAdminAuditLogLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxAdminAuditLogLimit {
			limit = n
		}
	}

	var cursor *repository.AdminAuditEventCursor
	if before := r.URL.Query().Get("beforeCreatedAt"); before != "" {
		t, err := time.Parse(time.RFC3339Nano, before)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid beforeCreatedAt -- expected RFC3339")
			return
		}
		id, err := strconv.ParseInt(r.URL.Query().Get("beforeId"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid or missing beforeId")
			return
		}
		cursor = &repository.AdminAuditEventCursor{CreatedAt: t, ID: id}
	}

	entries, next, err := h.svc.List(r.Context(), tenantID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := struct {
		Events     []service.AdminAuditLogEntry `json:"events"`
		NextCursor *struct {
			CreatedAt time.Time `json:"createdAt"`
			ID        int64     `json:"id"`
		} `json:"nextCursor"`
	}{Events: entries}
	if next != nil {
		resp.NextCursor = &struct {
			CreatedAt time.Time `json:"createdAt"`
			ID        int64     `json:"id"`
		}{CreatedAt: next.CreatedAt, ID: next.ID}
	}
	writeJSON(w, http.StatusOK, resp)
}
