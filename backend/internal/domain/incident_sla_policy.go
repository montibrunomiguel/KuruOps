package domain

import (
	"time"

	"github.com/google/uuid"
)

// IncidentSLAPolicy configures how many minutes an incident of a given
// (Severity, Priority) pair has before it's considered SLA breached. No
// row for a pair means unconfigured, not zero minutes -- see
// IncidentSLAService.Lookup.
type IncidentSLAPolicy struct {
	ID               uuid.UUID        `json:"id"`
	TenantID         uuid.UUID        `json:"tenantId"`
	Severity         Severity         `json:"severity"`
	Priority         IncidentPriority `json:"priority"`
	DueWithinMinutes int              `json:"dueWithinMinutes"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
}
