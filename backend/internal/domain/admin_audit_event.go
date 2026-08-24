package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AdminAuditEvent is an append-only record of an admin changing something
// in Settings -- see admin_audit_events in
// db/migrations/0007_admin_audit_events.up.sql. Separate from
// AlertEvent/IncidentEvent (which cover alert/incident state changes, not
// admin configuration) and from AuditEvent (the CEF/SIEM export's shape,
// which is a read-side projection over four different source tables, not a
// table of its own). Inserted in the same transaction as the state change
// it records, right after the state-changing repository call succeeds --
// same discipline as AlertEvent/IncidentEvent, so a failed audit write
// rolls back the state change too, rather than the two silently drifting
// apart.
//
// ActorID is non-nullable (unlike AlertEvent.ActorID) -- every one of the
// ~15 settings services this feeds from is triggered by an authenticated
// admin's own action on their own Settings page; there is no system- or
// AI-triggered write among them.
type AdminAuditEvent struct {
	ID        int64           `json:"id"`
	TenantID  uuid.UUID       `json:"tenantId"`
	Area      string          `json:"area"`
	Action    string          `json:"action"`
	ActorType ActorType       `json:"actorType"`
	ActorID   uuid.UUID       `json:"actorId"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
}
