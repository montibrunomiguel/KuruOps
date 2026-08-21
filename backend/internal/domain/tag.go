package domain

import (
	"time"

	"github.com/google/uuid"
)

// Tag mirrors `tags` -- the Settings-managed catalog of tag names an
// analyst can attach to an alert/incident, or that cmd/ingest will accept
// from a webhook payload. alerts.tags/incidents.tags stay plain text[]
// columns (no FK to this table -- see db/migrations/0001_initial_schema.up.sql
// for why), so this catalog is enforced at the API layer, not the database.
type Tag struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenantId"`
	Name      string     `json:"name"`
	Color     *string    `json:"color,omitempty"`
	CreatedBy *uuid.UUID `json:"createdBy,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}
