package domain

import (
	"time"

	"github.com/google/uuid"
)

// Tenant mirrors `tenants`. Unlike every other table in this codebase, it
// is intentionally not RLS-scoped (see db/migrations/0008 comment) — a
// tenant's own identity has to be resolvable before app.tenant_id can be
// set, which is exactly the login bootstrap problem Slug solves.
type Tenant struct {
	ID        uuid.UUID
	Name      string
	Slug      string
	Timezone  string
	CreatedAt time.Time
}
