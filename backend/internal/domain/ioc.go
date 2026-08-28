package domain

import (
	"time"

	"github.com/google/uuid"
)

// IOCType is a category of Indicator of Compromise. NIST SP 800-61r3
// defines an indicator of compromise as "a technical artifact ... that
// suggests that an attack is imminent, is currently underway, or has
// already occurred" (adapted from NIST SP 800-150, "Guide to Cyber Threat
// Information Sharing"), and gives five illustrative examples: an IP
// address, a DNS domain name, a URL, a file hash, and an email subject
// line. https://csrc.nist.gov/glossary/term/indicator_of_compromise --
// SP 800-150 in turn points to STIX 2.1 for structuring these for
// machine-readable exchange, which is where the rest of this list comes
// from (registry keys, mutexes, process names, user agents, and CVE IDs
// are all standard STIX Cyber Observable Object types this app's own SOC
// analysts routinely record during triage, well beyond NIST's five
// examples). "other" is the deliberate escape hatch for anything that
// doesn't fit -- this list is a starting point, not a closed set.
type IOCType string

const (
	IOCTypeIPAddress              IOCType = "ip_address"
	IOCTypeDomainName             IOCType = "domain_name"
	IOCTypeURL                    IOCType = "url"
	IOCTypeFileHash               IOCType = "file_hash"
	IOCTypeEmailAddress           IOCType = "email_address"
	IOCTypeEmailSubject           IOCType = "email_subject"
	IOCTypeFileName               IOCType = "file_name"
	IOCTypeFilePath               IOCType = "file_path"
	IOCTypeRegistryKey            IOCType = "registry_key"
	IOCTypeMutex                  IOCType = "mutex"
	IOCTypeProcessName            IOCType = "process_name"
	IOCTypeUserAgent              IOCType = "user_agent"
	IOCTypeCVE                    IOCType = "cve"
	IOCTypeCertificateFingerprint IOCType = "certificate_fingerprint"
	IOCTypeOther                  IOCType = "other"
)

// ValidIOCTypes is the complete, ordered set IOCTypeIsValid checks
// against -- ordered the same way the frontend's type dropdown presents
// them (roughly NIST's own five examples first, then the STIX-derived
// additions, "other" last as the catch-all).
var ValidIOCTypes = []IOCType{
	IOCTypeIPAddress,
	IOCTypeDomainName,
	IOCTypeURL,
	IOCTypeFileHash,
	IOCTypeEmailAddress,
	IOCTypeEmailSubject,
	IOCTypeFileName,
	IOCTypeFilePath,
	IOCTypeRegistryKey,
	IOCTypeMutex,
	IOCTypeProcessName,
	IOCTypeUserAgent,
	IOCTypeCVE,
	IOCTypeCertificateFingerprint,
	IOCTypeOther,
}

// IOCTypeIsValid reports whether t is one of ValidIOCTypes -- checked by
// IncidentService.AddIOC before insert, the same "reject an unknown enum
// value at the service layer, not just via a DB constraint" discipline
// applied to every other enum-shaped field in this codebase.
func IOCTypeIsValid(t IOCType) bool {
	for _, v := range ValidIOCTypes {
		if v == t {
			return true
		}
	}
	return false
}

// IOC is one Indicator of Compromise recorded against an incident --
// scoped to exactly one incident (not shared/reusable across incidents,
// unlike Tag), append-only (no update/delete endpoint) same as
// IncidentComment, whose CreatedBy/CreatedAt already double as the audit
// trail for who added it and when without needing a separate
// IncidentEvent row.
type IOC struct {
	ID          uuid.UUID `json:"id"`
	IncidentID  uuid.UUID `json:"incidentId"`
	TenantID    uuid.UUID `json:"tenantId"`
	Type        IOCType   `json:"type"`
	Value       string    `json:"value"`
	Description string    `json:"description"`
	// IdentifiedAt is when the analyst determined this indicator was
	// associated with the incident -- distinct from CreatedAt (when the
	// record was entered into KuruOps), which can lag behind it, e.g. an
	// indicator identified during initial triage but only logged here once
	// the analyst has a moment to write it up.
	IdentifiedAt time.Time `json:"identifiedAt"`
	CreatedBy    uuid.UUID `json:"createdBy"`
	// CreatedByName is denormalized at write time, same reasoning as
	// IncidentComment.AuthorName -- see that field's doc comment.
	CreatedByName string    `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
}
