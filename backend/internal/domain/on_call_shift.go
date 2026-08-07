package domain

import (
	"time"

	"github.com/google/uuid"
)

// OnCallShift mirrors `on_call_shifts` -- one weekly recurring coverage
// window (weekday + minute-of-day range, in the tenant's configured
// timezone) assigning a single analyst. See the migration's comment for why
// StartMinute/EndMinute are plain minutes-since-midnight rather than a
// Postgres `time` column, and why overlapping shifts are allowed rather than
// rejected.
type OnCallShift struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenantId"`
	UserID      uuid.UUID `json:"userId"`
	UserName    string    `json:"userName"`
	Weekday     int       `json:"weekday"`
	StartMinute int       `json:"startMinute"`
	EndMinute   int       `json:"endMinute"`
	CreatedAt   time.Time `json:"createdAt"`
}
