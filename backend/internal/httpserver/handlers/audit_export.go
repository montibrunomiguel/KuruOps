package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/argusops/argusops/internal/httpserver/middleware"
	"github.com/argusops/argusops/internal/service"
)

const (
	defaultAuditExportLimit = 1000
	maxAuditExportLimit     = 10000
)

// AuditExportHandlers is Settings -> Audit Export: admin-only, same gate as
// every other /settings/... route.
type AuditExportHandlers struct {
	svc *service.AuditExportService
}

func NewAuditExportHandlers(svc *service.AuditExportService) *AuditExportHandlers {
	return &AuditExportHandlers{svc: svc}
}

func (h *AuditExportHandlers) Routes(r chi.Router) {
	r.Get("/cef", h.exportCEF)
}

// exportCEF serves a downloadable CEF log of the tenant's alert/incident
// event history. sinceCreatedAt/sinceEventId are optional keyset cursor
// params for resuming a paginated pull (see service.ExportCursor) -- a
// plain "Export as CEF" button in the UI just omits them and gets the
// first page; a scripted integration reads the X-Next-Cursor-* response
// headers to keep paging until a response with no next-cursor header
// (X-Next-Cursor-Created-At absent) signals the end.
func (h *AuditExportHandlers) exportCEF(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := middleware.TenantID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing tenant context")
		return
	}

	limit := defaultAuditExportLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxAuditExportLimit {
			limit = n
		}
	}

	var cursor *service.ExportCursor
	if since := r.URL.Query().Get("sinceCreatedAt"); since != "" {
		t, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid sinceCreatedAt -- expected RFC3339")
			return
		}
		cursor = &service.ExportCursor{CreatedAt: t, EventID: r.URL.Query().Get("sinceEventId")}
	}

	lines, next, err := h.svc.ExportCEF(r.Context(), tenantID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if next != nil {
		w.Header().Set("X-Next-Cursor-Created-At", next.CreatedAt.Format(time.RFC3339Nano))
		w.Header().Set("X-Next-Cursor-Event-Id", next.EventID)
	}
	filename := fmt.Sprintf("argusops-audit-%s.cef.log", time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}
