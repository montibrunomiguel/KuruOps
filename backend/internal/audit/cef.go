// Package audit formats ArgusOps' append-only alert/incident event log as
// CEF (Common Event Format) -- a single-line-per-event text format most
// SIEMs (Splunk, ArcSight, QRadar...) can ingest directly, whether pulled
// via the export endpoint or fed into a log shipper pointed at the
// downloaded file. See handlers.AuditExportHandlers for the pull/download
// endpoint this backs; a persistent push-to-syslog connection is
// deliberately out of scope for v1 (no new long-lived-connection
// infrastructure needed for a first cut -- see PLANO_DE_MELHORIAS.md).
package audit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/argusops/argusops/internal/domain"
)

const (
	cefVersion    = "0"
	deviceVendor  = "ArgusOps"
	deviceProduct = "ArgusOps"
	deviceVersion = "1.0"
	// cefSeverity is fixed for every event: this log is an audit trail of
	// what changed (status/phase transitions, comments, tag edits...), not
	// a re-classification of the underlying alert/incident's own severity
	// -- CEF's severity field describes the event's own urgency, and
	// "something changed" is uniformly low/informational regardless of
	// which alert it happened on.
	cefSeverity = "3"
)

// FormatCEF renders one AuditEvent as a single CEF line (no trailing
// newline -- callers join lines with "\n"). Signature ID is
// "<kind>.<eventType>" (e.g. "alert.status_changed"), giving each distinct
// event type a stable identifier a SIEM can filter/alert on.
func FormatCEF(e domain.AuditEvent) string {
	name := fmt.Sprintf("%s: %s", cefEscapeHeader(e.ContextTitle), cefEscapeHeader(humanizeEventType(e.EventType)))
	signatureID := e.Kind + "." + e.EventType

	actor := "system"
	if e.ActorID != nil {
		actor = e.ActorID.String()
	}

	extensions := []string{
		"rt=" + strconv.FormatInt(e.CreatedAt.UnixMilli(), 10),
		"cat=" + cefEscapeExtension(e.Kind),
		"suser=" + cefEscapeExtension(actor),
		"outcome=" + cefEscapeExtension(string(e.ActorType)),
		"cs1Label=contextId", "cs1=" + cefEscapeExtension(e.ContextID.String()),
		"cs2Label=contextTitle", "cs2=" + cefEscapeExtension(e.ContextTitle),
		"cs3Label=eventData", "cs3=" + cefEscapeExtension(string(e.Data)),
	}

	return fmt.Sprintf("CEF:%s|%s|%s|%s|%s|%s|%s|%s",
		cefVersion, cefEscapeHeader(deviceVendor), cefEscapeHeader(deviceProduct), cefEscapeHeader(deviceVersion),
		cefEscapeHeader(signatureID), name, cefSeverity, strings.Join(extensions, " "),
	)
}

// humanizeEventType turns "status_changed" into "status changed" -- CEF's
// Name field is meant to be human-readable, the raw snake_case event type
// already carries the same information in the Signature ID field.
func humanizeEventType(eventType string) string {
	return strings.ReplaceAll(eventType, "_", " ")
}

// cefEscapeHeader escapes the pipe-delimited header fields (Device Vendor
// through Name) per the CEF spec: backslash and pipe must be escaped.
func cefEscapeHeader(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `|`, `\|`)
	return s
}

// cefEscapeExtension escapes an Extension field's value per the CEF spec:
// backslash, equals sign, and newlines must be escaped -- pipe does NOT
// need escaping here (only in the header).
func cefEscapeExtension(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `=`, `\=`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\n`)
	return s
}
