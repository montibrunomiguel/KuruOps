package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

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
	r.Get("/json", h.exportJSON)
}

// exportQueryParams reads the limit/cursor query params exportCEF and
// exportJSON both accept -- see service.ExportCursor's doc comment for the
// pagination contract. ok is false if a bad cursor param was already
// written as an error response and the caller should return immediately.
func exportQueryParams(w http.ResponseWriter, r *http.Request) (limit int, cursor *service.ExportCursor, ok bool) {
	limit = defaultAuditExportLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxAuditExportLimit {
			limit = n
		}
	}

	if since := r.URL.Query().Get("sinceCreatedAt"); since != "" {
		t, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid sinceCreatedAt -- expected RFC3339")
			return 0, nil, false
		}
		cursor = &service.ExportCursor{CreatedAt: t, EventID: r.URL.Query().Get("sinceEventId")}
	}
	return limit, cursor, true
}

func setNextCursorHeaders(w http.ResponseWriter, next *service.ExportCursor) {
	if next != nil {
		w.Header().Set("X-Next-Cursor-Created-At", next.CreatedAt.Format(time.RFC3339Nano))
		w.Header().Set("X-Next-Cursor-Event-Id", next.EventID)
	}
}

// exportCEF serves a downloadable CEF log of the tenant's alert/incident
// event history. sinceCreatedAt/sinceEventId are optional keyset cursor
// params for resuming a paginated pull (see service.ExportCursor) -- a
// plain "Export as CEF" button in the UI just omits them and gets the
// first page; a scripted integration reads the X-Next-Cursor-* response
// headers to keep paging until a response with no next-cursor header
// (X-Next-Cursor-Created-At absent) signals the end.
func (h *AuditExportHandlers) exportCEF(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := exportQueryParams(w, r)
	if !ok {
		return
	}

	lines, next, err := h.svc.ExportCEF(r.Context(), tenantID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	setNextCursorHeaders(w, next)
	filename := fmt.Sprintf("argusops-audit-%s.cef.log", time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

// exportJSON is exportCEF's counterpart for tooling that wants structured
// data -- same events, same cursor mechanics, newline-delimited JSON
// (one domain.AuditEvent object per line) instead of CEF log lines. ndjson
// rather than a single JSON array keeps the same streaming/pagination-
// friendly shape CEF already has -- a consumer can process each line as it
// arrives instead of buffering the whole response to parse one array.
func (h *AuditExportHandlers) exportJSON(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	limit, cursor, ok := exportQueryParams(w, r)
	if !ok {
		return
	}

	events, next, err := h.svc.ExportJSON(r.Context(), tenantID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	setNextCursorHeaders(w, next)
	filename := fmt.Sprintf("argusops-audit-%s.ndjson", time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	for _, e := range events {
		_ = enc.Encode(e) // one JSON object per line; a mid-stream encode error can't be reported after headers are already sent
	}
}
