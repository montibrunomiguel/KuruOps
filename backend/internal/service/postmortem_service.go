package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kuruops/kuruops/internal/domain"
)

// postmortemSystemPrompt asks for a short executive summary only -- root
// cause, impact, key learnings -- not a full report; the structured
// template PostmortemService.Generate builds around it already covers
// every other section (timeline, roles, tags, SLA, linked alerts, team
// notes in full).
const postmortemSystemPrompt = `You are a SOC (Security Operations Center) analyst assistant. Given the full record of a security incident (description, phase timeline, team notes, linked alerts, SLA outcome), write a short executive summary for a postmortem document: likely root cause, business/security impact, and the key lessons learned. Three short paragraphs at most, plain prose, no headings or bullet lists -- the surrounding document already provides structure.`

var phaseLabels = map[domain.IncidentPhase]string{
	domain.PhaseNew:               "New",
	domain.PhaseDetectionAnalysis: "Detection & Analysis",
	domain.PhaseContainment:       "Containment",
	domain.PhaseEradication:       "Eradication",
	domain.PhaseRecovery:          "Recovery",
	domain.PhasePostIncident:      "Post-Incident",
}

var roleLabels = map[domain.IncidentRole]string{
	domain.RoleCommander:          "Incident Commander",
	domain.RoleTechnicalLead:      "Technical Lead",
	domain.RoleIncidentHandler:    "Incident Handler",
	domain.RoleCommunicationsLead: "Communications Lead",
	domain.RolePrivacyOfficer:     "Privacy Officer",
}

// iocTypeLabels renders domain.IOCType's snake_case wire values as the
// same human-readable labels the frontend's i18n strings show -- see
// domain.IOCType's doc comment for where this list comes from.
var iocTypeLabels = map[domain.IOCType]string{
	domain.IOCTypeIPAddress:              "IP Address",
	domain.IOCTypeDomainName:             "Domain Name",
	domain.IOCTypeURL:                    "URL",
	domain.IOCTypeFileHash:               "File Hash",
	domain.IOCTypeEmailAddress:           "Email Address",
	domain.IOCTypeEmailSubject:           "Email Subject",
	domain.IOCTypeFileName:               "File Name",
	domain.IOCTypeFilePath:               "File Path",
	domain.IOCTypeRegistryKey:            "Registry Key",
	domain.IOCTypeMutex:                  "Mutex",
	domain.IOCTypeProcessName:            "Process Name",
	domain.IOCTypeUserAgent:              "User-Agent",
	domain.IOCTypeCVE:                    "CVE",
	domain.IOCTypeCertificateFingerprint: "Certificate Fingerprint",
	domain.IOCTypeOther:                  "Other",
}

func iocTypeLabel(t domain.IOCType) string {
	if label := iocTypeLabels[t]; label != "" {
		return label
	}
	return string(t)
}

// PostmortemService builds the Markdown document GET
// /api/v1/incidents/{id}/postmortem serves: a structured template that
// always compiles every piece of information already on the incident
// detail page (phase timeline with durations, team roles, tags, SLA,
// linked alerts, team notes in full), with an AI-generated executive
// summary prepended when the tenant has an LLM provider configured (see
// executiveSummary) -- silently omitted, not an error, when it doesn't;
// the rest of the document is already complete without it. Same
// fail-gracefully-without-AI principle as auto-analyze-on-ingest.
type PostmortemService struct {
	incidents *IncidentService
	ai        *AIAnalysisService
}

func NewPostmortemService(incidents *IncidentService, ai *AIAnalysisService) *PostmortemService {
	return &PostmortemService{incidents: incidents, ai: ai}
}

// Generate aggregates the incident's full record and renders it as
// Markdown. found is false if the incident doesn't exist or isn't visible
// under allowedTags, mirroring IncidentService.Get's own (nil, nil)
// not-found contract.
func (s *PostmortemService) Generate(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) (doc string, found bool, err error) {
	inc, err := s.incidents.Get(ctx, tenantID, incidentID, allowedTags)
	if err != nil {
		return "", false, fmt.Errorf("load incident: %w", err)
	}
	if inc == nil {
		return "", false, nil
	}

	// found is discarded on each sub-resource load below for the same reason
	// IncidentReportService.GeneratePDF discards it -- Get above already
	// established visibility under allowedTags.
	history, _, err := s.incidents.StatusHistory(ctx, tenantID, incidentID, allowedTags)
	if err != nil {
		return "", false, fmt.Errorf("load status history: %w", err)
	}
	comments, _, err := s.incidents.Comments(ctx, tenantID, incidentID, allowedTags)
	if err != nil {
		return "", false, fmt.Errorf("load comments: %w", err)
	}
	linkedAlerts, _, err := s.incidents.LinkedAlerts(ctx, tenantID, incidentID, allowedTags)
	if err != nil {
		return "", false, fmt.Errorf("load linked alerts: %w", err)
	}
	iocs, _, err := s.incidents.IOCs(ctx, tenantID, incidentID, allowedTags)
	if err != nil {
		return "", false, fmt.Errorf("load iocs: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Postmortem: %s\n\n", inc.Title)

	if summary := s.executiveSummary(ctx, tenantID, inc, history, comments, linkedAlerts); summary != "" {
		fmt.Fprintf(&b, "## Executive Summary (AI-generated)\n\n%s\n\n", summary)
	}

	renderOverview(&b, inc)
	renderTimeline(&b, inc, history)
	renderRoles(&b, inc)
	renderLinkedAlerts(&b, linkedAlerts)
	renderIOCs(&b, iocs)
	renderTeamNotes(&b, comments)
	if inc.LatestAnalysis != nil && *inc.LatestAnalysis != "" {
		fmt.Fprintf(&b, "## Latest AI Analysis\n\n%s\n\n", *inc.LatestAnalysis)
	}

	return b.String(), true, nil
}

// executiveSummary returns "" whenever it can't produce one -- no LLM
// provider configured, secret resolution failure, or the completion call
// itself erroring -- so a missing/misconfigured AI integration never blocks
// the rest of the (already complete) document from being generated.
func (s *PostmortemService) executiveSummary(
	ctx context.Context,
	tenantID uuid.UUID,
	inc *domain.Incident,
	history []domain.IncidentStatusHistoryEntry,
	comments []domain.IncidentComment,
	linkedAlerts []domain.Alert,
) string {
	client, err := s.ai.BuildClient(ctx, tenantID)
	if err != nil {
		return ""
	}
	text, err := client.Complete(ctx, postmortemSystemPrompt, postmortemPrompt(inc, history, comments, linkedAlerts))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

func postmortemPrompt(
	inc *domain.Incident,
	history []domain.IncidentStatusHistoryEntry,
	comments []domain.IncidentComment,
	linkedAlerts []domain.Alert,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n", inc.Title)
	if inc.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", inc.Description)
	}
	fmt.Fprintf(&b, "Severity: %s\n", inc.Severity)
	fmt.Fprintf(&b, "Priority: %s\n", inc.Priority)
	if len(inc.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(inc.Tags, ", "))
	}
	fmt.Fprintf(&b, "SLA breached: %t\n", inc.SLABreached)

	if len(history) > 0 {
		b.WriteString("\nPhase timeline:\n")
		for _, phase := range orderedHistory(history) {
			fmt.Fprintf(&b, "- %s entered at %s\n", phaseLabels[phase.Phase], phase.EffectiveEnteredAt().Format(time.RFC3339))
		}
	}

	if len(linkedAlerts) > 0 {
		b.WriteString("\nLinked alerts:\n")
		for _, a := range linkedAlerts {
			fmt.Fprintf(&b, "- %s (severity %s)\n", a.Title, a.Severity)
		}
	}

	if len(comments) > 0 {
		b.WriteString("\nTeam notes:\n")
		for _, c := range comments {
			fmt.Fprintf(&b, "- %s: %s\n", c.AuthorName, c.Body)
		}
	}

	return b.String()
}

// orderedHistory sorts entries into NISTPhaseOrder, matching how the
// frontend's StatusHistoryPanel presents them -- the rows themselves are
// already one-per-phase-entered, but not guaranteed to come back from the
// repository in phase order.
func orderedHistory(history []domain.IncidentStatusHistoryEntry) []domain.IncidentStatusHistoryEntry {
	byPhase := make(map[domain.IncidentPhase]domain.IncidentStatusHistoryEntry, len(history))
	for _, e := range history {
		byPhase[e.Phase] = e
	}
	ordered := make([]domain.IncidentStatusHistoryEntry, 0, len(history))
	for _, phase := range domain.NISTPhaseOrder {
		if e, ok := byPhase[phase]; ok {
			ordered = append(ordered, e)
		}
	}
	return ordered
}

func renderOverview(b *strings.Builder, inc *domain.Incident) {
	fmt.Fprintf(b, "- **Severity:** %s\n", inc.Severity)
	fmt.Fprintf(b, "- **Priority:** %s\n", strings.ToUpper(string(inc.Priority)))
	fmt.Fprintf(b, "- **Opened:** %s\n", inc.OpenedAt.Format(time.RFC3339))
	if inc.ClosedAt != nil {
		fmt.Fprintf(b, "- **Closed:** %s\n", inc.ClosedAt.Format(time.RFC3339))
	}
	if inc.SLADueAt != nil {
		fmt.Fprintf(b, "- **SLA due:** %s (%s)\n", inc.SLADueAt.Format(time.RFC3339), slaOutcome(inc))
	}
	if len(inc.Tags) > 0 {
		fmt.Fprintf(b, "- **Tags:** %s\n", strings.Join(inc.Tags, ", "))
	}
	if inc.Description != "" {
		fmt.Fprintf(b, "\n%s\n", inc.Description)
	}
	b.WriteString("\n")
}

func slaOutcome(inc *domain.Incident) string {
	if inc.SLABreached {
		return "breached"
	}
	return "met"
}

// renderTimeline lists each phase the incident actually entered (in NIST
// order) with how long it stayed there -- the duration between one phase's
// effective entry and the next's, or up to ClosedAt for the final phase if
// the incident is closed. A correction (CorrectedEnteredAt) is called out
// alongside its reason, same chain-of-custody transparency the frontend's
// StatusHistoryPanel gives an analyst correcting a timestamp.
func renderTimeline(b *strings.Builder, inc *domain.Incident, history []domain.IncidentStatusHistoryEntry) {
	ordered := orderedHistory(history)
	if len(ordered) == 0 {
		return
	}
	b.WriteString("## Phase Timeline\n\n")
	for i, entry := range ordered {
		start := entry.EffectiveEnteredAt()
		fmt.Fprintf(b, "- **%s** -- entered %s", phaseLabels[entry.Phase], start.Format(time.RFC3339))

		var end *time.Time
		if i+1 < len(ordered) {
			next := ordered[i+1].EffectiveEnteredAt()
			end = &next
		} else if inc.ClosedAt != nil {
			end = inc.ClosedAt
		}
		if end != nil {
			fmt.Fprintf(b, ", duration %s", formatDuration(end.Sub(start)))
		} else {
			b.WriteString(", ongoing")
		}
		b.WriteString("\n")

		if entry.CorrectedEnteredAt != nil {
			reason := ""
			if entry.CorrectionReason != nil {
				reason = *entry.CorrectionReason
			}
			fmt.Fprintf(b, "  - *Corrected from %s: %s*\n", entry.EnteredAt.Format(time.RFC3339), reason)
		}
	}
	b.WriteString("\n")
}

func renderRoles(b *strings.Builder, inc *domain.Incident) {
	if len(inc.Roles) == 0 {
		return
	}
	b.WriteString("## Team Roles\n\n")
	for _, assignment := range inc.Roles {
		label := roleLabels[assignment.Role]
		if label == "" {
			label = string(assignment.Role)
		}
		fmt.Fprintf(b, "- **%s:** %s\n", label, assignment.User.Name)
	}
	b.WriteString("\n")
}

func renderLinkedAlerts(b *strings.Builder, alerts []domain.Alert) {
	b.WriteString("## Linked Alerts\n\n")
	if len(alerts) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	for _, a := range alerts {
		fmt.Fprintf(b, "- **%s** (%s, %s) -- %s\n", a.Title, a.Severity, a.Status, a.Source)
	}
	b.WriteString("\n")
}

// renderIOCs, like renderLinkedAlerts, always gets a header (even with
// zero IOCs recorded) -- a report/postmortem meant to be read standalone
// should say "none identified" explicitly rather than silently omit the
// section.
func renderIOCs(b *strings.Builder, iocs []domain.IOC) {
	b.WriteString("## Indicators of Compromise (IOCs)\n\n")
	if len(iocs) == 0 {
		b.WriteString("None identified.\n\n")
		return
	}
	for _, i := range iocs {
		fmt.Fprintf(b, "- **[%s]** `%s` -- identified %s", iocTypeLabel(i.Type), i.Value, i.IdentifiedAt.Format("2006-01-02"))
		if i.Description != "" {
			fmt.Fprintf(b, ": %s", i.Description)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func renderTeamNotes(b *strings.Builder, comments []domain.IncidentComment) {
	b.WriteString("## Team Notes\n\n")
	if len(comments) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	for _, c := range comments {
		fmt.Fprintf(b, "**%s** (%s):\n%s\n\n", c.AuthorName, c.CreatedAt.Format(time.RFC3339), c.Body)
	}
}

// formatDuration renders a phase's dwell time in the coarsest unit that
// keeps it readable -- same s/m/h/d escalation as the frontend's own
// lib/format.ts#formatDuration, kept independent since this runs
// server-side with no shared module between the two.
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d.Round(time.Second).Seconds())
	if totalSeconds < 60 {
		return fmt.Sprintf("%ds", totalSeconds)
	}
	minutes := totalSeconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 48 {
		return fmt.Sprintf("%dh%dm", hours, minutes%60)
	}
	return fmt.Sprintf("%dd", hours/24)
}
