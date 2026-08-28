package service

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jung-kurt/gofpdf"

	"github.com/argusops/argusops/internal/domain"
)

// pdfLineHeight is the default row height (mm) for a single line of body
// text across every section below -- kept as one constant so every
// section's vertical rhythm stays consistent without repeating the literal.
const pdfLineHeight = 6.0

// IncidentReportService builds the PDF document GET
// /api/v1/incidents/{id}/report.pdf serves -- a PDF twin of
// PostmortemService.Generate: same aggregation (Get + StatusHistory +
// Comments + LinkedAlerts), and reuses that file's phaseLabels/roleLabels/
// orderedHistory/formatDuration/slaOutcome helpers rather than
// redefining them, just rendered as a formatted PDF instead of Markdown.
// Unlike the postmortem, this has no AI-generated executive summary --
// it's meant as a point-in-time factual export (for a stakeholder, an
// auditor, an incident retro), not a narrative document, so every run
// against the same incident state produces the same bytes.
type IncidentReportService struct {
	incidents *IncidentService
}

func NewIncidentReportService(incidents *IncidentService) *IncidentReportService {
	return &IncidentReportService{incidents: incidents}
}

// GeneratePDF aggregates the incident's full record and renders it as a
// PDF. found is false if the incident doesn't exist or isn't visible under
// allowedTags, mirroring IncidentService.Get's own (nil, nil) not-found
// contract -- same shape as PostmortemService.Generate.
func (s *IncidentReportService) GeneratePDF(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) (pdfBytes []byte, found bool, err error) {
	inc, err := s.incidents.Get(ctx, tenantID, incidentID, allowedTags)
	if err != nil {
		return nil, false, fmt.Errorf("load incident: %w", err)
	}
	if inc == nil {
		return nil, false, nil
	}

	history, err := s.incidents.StatusHistory(ctx, tenantID, incidentID)
	if err != nil {
		return nil, false, fmt.Errorf("load status history: %w", err)
	}
	comments, err := s.incidents.Comments(ctx, tenantID, incidentID)
	if err != nil {
		return nil, false, fmt.Errorf("load comments: %w", err)
	}
	linkedAlerts, err := s.incidents.LinkedAlerts(ctx, tenantID, incidentID)
	if err != nil {
		return nil, false, fmt.Errorf("load linked alerts: %w", err)
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	// isUTF8=true: inc.Title is a real UTF-8 Go string (unlike the content-
	// stream text below, PDF metadata fields like /Title take a UTF8 flag
	// gofpdf itself uses to pick UTF-16BE encoding -- passing false here
	// would embed inc.Title's raw UTF-8 bytes as if they were Latin-1/
	// PDFDocEncoding, the same class of mojibake bug as the content-stream
	// text tr() below fixes, just in a PDF viewer's title bar/properties
	// dialog instead of the visible page.
	pdf.SetTitle("Incident Report: "+inc.Title, true)
	pdf.SetAuthor("ArgusOps", false)
	// Uncompressed: these reports are short (a handful of KB at most), so
	// the size cost is negligible, and it keeps the generated PDF's content
	// stream text-searchable with a plain byte-string tool (grep/strings,
	// or a test asserting on the raw bytes) instead of needing a PDF parser
	// just to confirm what the report actually says.
	pdf.SetCompression(false)
	pdf.SetMargins(18, 18, 18)
	pdf.AddPage()

	// gofpdf's built-in "Arial" is a PDF standard font, which the spec (and
	// gofpdf itself, see SetFont's doc comment) requires to be interpreted
	// as cp1252 -- every string reaching CellFormat/MultiCell has to be
	// translated from Go's native UTF-8 into that encoding first, or any
	// non-ASCII character (e.g. the accents this app's own pt-BR locale
	// uses -- ã, ç, é) comes out as mojibake, one mangled glyph per UTF-8
	// continuation byte. tr is that translator; every render*/pdfKV helper
	// below takes it and applies it to each piece of incident-derived text
	// before handing it to gofpdf (fixed English section labels are ASCII,
	// so translating them too is harmless -- simpler than tracking which
	// strings need it).
	tr := pdf.UnicodeTranslatorFromDescriptor("")

	renderPDFHeader(pdf, tr, inc)
	renderPDFOverview(pdf, tr, inc)
	renderPDFTimeline(pdf, tr, inc, history)
	renderPDFRoles(pdf, tr, inc)
	renderPDFLinkedAlerts(pdf, tr, linkedAlerts)
	renderPDFTeamNotes(pdf, tr, comments)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, false, fmt.Errorf("render pdf: %w", err)
	}
	return buf.Bytes(), true, nil
}

func renderPDFHeader(pdf *gofpdf.Fpdf, tr func(string) string, inc *domain.Incident) {
	pdf.SetFont("Arial", "B", 18)
	pdf.MultiCell(0, 9, tr(inc.Title), "", "L", false)
	pdf.SetFont("Arial", "", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.CellFormat(0, 5, "Incident Report -- generated "+time.Now().UTC().Format("2006-01-02 15:04")+" UTC", "", 1, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(3)
}

func pdfSectionHeader(pdf *gofpdf.Fpdf, title string) {
	pdf.Ln(3)
	pdf.SetFont("Arial", "B", 13)
	pdf.CellFormat(0, 8, title, "B", 1, "L", false, 0, "")
	pdf.Ln(2)
}

// pdfKV renders a "Label: value" row as two cells on one line -- the label
// in a fixed-width bold column so every row in a section lines up, the
// value taking the rest of the line. Every field this is used for is a
// short single-line value (severity, a date, a tag list); Description and
// team-note bodies (which can be long/multi-line) use MultiCell directly
// instead, further below. value is expected to already be translated (tr)
// by the caller when it's incident-derived text, not a label constant.
func pdfKV(pdf *gofpdf.Fpdf, label, value string) {
	pdf.SetFont("Arial", "B", 11)
	pdf.CellFormat(38, pdfLineHeight, label+":", "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 11)
	pdf.CellFormat(0, pdfLineHeight, value, "", 1, "L", false, 0, "")
}

func renderPDFOverview(pdf *gofpdf.Fpdf, tr func(string) string, inc *domain.Incident) {
	pdfSectionHeader(pdf, "Overview")

	pdfKV(pdf, "Severity", strings.ToUpper(string(inc.Severity)))
	pdfKV(pdf, "Priority", strings.ToUpper(string(inc.Priority)))
	pdfKV(pdf, "Phase", phaseLabels[inc.Phase])
	pdfKV(pdf, "Opened", inc.OpenedAt.Format(time.RFC3339))
	if inc.ClosedAt != nil {
		pdfKV(pdf, "Closed", inc.ClosedAt.Format(time.RFC3339))
	}
	if inc.SLADueAt != nil {
		pdfKV(pdf, "SLA due", fmt.Sprintf("%s (%s)", inc.SLADueAt.Format(time.RFC3339), slaOutcome(inc)))
	}
	if len(inc.Tags) > 0 {
		pdfKV(pdf, "Tags", tr(strings.Join(inc.Tags, ", ")))
	}

	if inc.Description != "" {
		pdf.Ln(2)
		pdf.SetFont("Arial", "B", 11)
		pdf.CellFormat(0, pdfLineHeight, "Description", "", 1, "L", false, 0, "")
		pdf.SetFont("Arial", "", 11)
		pdf.MultiCell(0, pdfLineHeight, tr(inc.Description), "", "L", false)
	}
}

// renderPDFTimeline is the PDF rendering of the exact same data
// PostmortemService.renderTimeline turns into Markdown -- see its doc
// comment for the duration/correction semantics, unchanged here.
func renderPDFTimeline(pdf *gofpdf.Fpdf, tr func(string) string, inc *domain.Incident, history []domain.IncidentStatusHistoryEntry) {
	ordered := orderedHistory(history)
	if len(ordered) == 0 {
		return
	}
	pdfSectionHeader(pdf, "Phase Timeline")

	for i, entry := range ordered {
		start := entry.EffectiveEnteredAt()
		line := fmt.Sprintf("- %s -- entered %s", phaseLabels[entry.Phase], start.Format(time.RFC3339))

		var end *time.Time
		if i+1 < len(ordered) {
			next := ordered[i+1].EffectiveEnteredAt()
			end = &next
		} else if inc.ClosedAt != nil {
			end = inc.ClosedAt
		}
		if end != nil {
			line += fmt.Sprintf(", duration %s", formatDuration(end.Sub(start)))
		} else {
			line += ", ongoing"
		}

		pdf.SetFont("Arial", "", 11)
		pdf.MultiCell(0, pdfLineHeight, line, "", "L", false)

		if entry.CorrectedEnteredAt != nil {
			reason := ""
			if entry.CorrectionReason != nil {
				reason = *entry.CorrectionReason
			}
			pdf.SetFont("Arial", "I", 10)
			pdf.MultiCell(0, 5, tr(fmt.Sprintf("    Corrected from %s: %s", entry.EnteredAt.Format(time.RFC3339), reason)), "", "L", false)
		}
	}
}

func renderPDFRoles(pdf *gofpdf.Fpdf, tr func(string) string, inc *domain.Incident) {
	if len(inc.Roles) == 0 {
		return
	}
	pdfSectionHeader(pdf, "Team Roles")
	for _, assignment := range inc.Roles {
		label := roleLabels[assignment.Role]
		if label == "" {
			label = string(assignment.Role)
		}
		pdfKV(pdf, label, tr(assignment.User.Name))
	}
}

// renderPDFLinkedAlerts, unlike the Markdown postmortem's version, always
// gets a header (even with zero linked alerts) -- a report meant to be
// printed/shared standalone should say "none" explicitly rather than
// silently omit the section, since there's no sibling page (like the
// incident detail view) around it to fall back on for context.
func renderPDFLinkedAlerts(pdf *gofpdf.Fpdf, tr func(string) string, alerts []domain.Alert) {
	pdfSectionHeader(pdf, "Linked Alerts")
	pdf.SetFont("Arial", "", 11)
	if len(alerts) == 0 {
		pdf.CellFormat(0, pdfLineHeight, "None.", "", 1, "L", false, 0, "")
		return
	}
	for _, a := range alerts {
		line := fmt.Sprintf("- %s (%s, %s) -- %s", a.Title, a.Severity, a.Status, a.Source)
		pdf.MultiCell(0, pdfLineHeight, tr(line), "", "L", false)
	}
}

func renderPDFTeamNotes(pdf *gofpdf.Fpdf, tr func(string) string, comments []domain.IncidentComment) {
	pdfSectionHeader(pdf, "Team Notes")
	if len(comments) == 0 {
		pdf.SetFont("Arial", "", 11)
		pdf.CellFormat(0, pdfLineHeight, "None.", "", 1, "L", false, 0, "")
		return
	}
	for _, c := range comments {
		pdf.SetFont("Arial", "B", 11)
		pdf.MultiCell(0, pdfLineHeight, tr(fmt.Sprintf("%s (%s):", c.AuthorName, c.CreatedAt.Format(time.RFC3339))), "", "L", false)
		pdf.SetFont("Arial", "", 11)
		pdf.MultiCell(0, pdfLineHeight, tr(c.Body), "", "L", false)
		pdf.Ln(1)
	}
}
