package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditEvent is one row of the tenant's append-only alert/incident event
// log (alert_events, incident_events, alert_comments, incident_comments),
// shaped for the CEF/SIEM export endpoint -- see
// AuditExportService/handlers.AuditExportHandlers and internal/audit's CEF
// formatter. EventID is this row's own primary key in whichever source
// table it came from -- those four tables don't share one ID space (two
// use a bigint identity, two use a uuid), so EventID is only ever used
// paired with CreatedAt as an export keyset cursor, never as a
// globally-unique identifier on its own.
type AuditEvent struct {
	EventID      string
	Kind         string // "alert" or "incident"
	ContextID    uuid.UUID
	ContextTitle string
	EventType    string
	ActorType    ActorType
	ActorID      *uuid.UUID
	Data         json.RawMessage
	CreatedAt    time.Time
}
