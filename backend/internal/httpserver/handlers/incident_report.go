package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/httpserver/middleware"
)

// reportPDF streams a generated PDF report for the incident -- same
// download-response shape as postmortemDoc/AuditExportHandlers.exportCEF
// (forced attachment, no JSON envelope). See
// IncidentReportService.GeneratePDF for what the document contains.
func (h *IncidentHandlers) reportPDF(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := mustTenantID(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid incident id")
		return
	}

	pdfBytes, found, err := h.report.GeneratePDF(r.Context(), tenantID, id, middleware.AllowedTags(r.Context()))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "incident not found")
		return
	}

	filename := fmt.Sprintf("incident-report-%s.pdf", id)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdfBytes)
}
