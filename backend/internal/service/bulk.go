package service

import "github.com/google/uuid"

// BulkResult reports the outcome of one item within a bulk operation.
// AlertService.BulkChangeStatus and IncidentService.BulkChangePhase apply
// their existing single-ID method (ChangeStatus/ChangePhase) in a loop
// rather than a new multi-row UPDATE, so every invariant those methods
// already enforce (tag-visibility, the "closed is terminal" guard, one
// AlertEvent/IncidentEvent per affected item) keeps working unmodified. A
// partial failure -- e.g. one of N selected items is no longer visible to
// the caller, or was closed by someone else in the meantime -- doesn't
// block the rest: each item's outcome is reported independently here
// instead of the whole request failing all-or-nothing.
type BulkResult struct {
	ID      uuid.UUID `json:"id"`
	Success bool      `json:"success"`
	Error   string    `json:"error,omitempty"`
}
