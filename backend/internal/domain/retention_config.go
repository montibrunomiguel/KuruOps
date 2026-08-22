package domain

import (
	"time"

	"github.com/google/uuid"
)

// DefaultRetentionMonths is how long a closed alert/incident stays in the
// tool before cmd/worker's sweepDataRetention permanently deletes it, when
// a tenant has never explicitly configured Settings -> Retention. Single
// source of truth -- RetentionConfigService.Get synthesizes it when no
// tenant_retention_config row exists, and the worker sweep's SQL falls back
// to the same value via COALESCE for the same reason.
const DefaultRetentionMonths = 18

// RetentionConfig mirrors `tenant_retention_config` (see
// db/migrations/0006_retention_config.up.sql). Unlike SMTPConfig/
// StorageConfig (no row = feature off), retention is on by default -- a nil
// repository row still produces a RetentionConfig with the default months
// applied, distinguished from an explicitly-saved one via Configured.
type RetentionConfig struct {
	TenantID                uuid.UUID `json:"tenantId"`
	AlertRetentionMonths    int       `json:"alertRetentionMonths"`
	IncidentRetentionMonths int       `json:"incidentRetentionMonths"`
	// Configured is false when these are RetentionConfigService.Get's
	// synthesized defaults, not a value an admin ever actually saved --
	// lets the frontend show "using the default" versus a real saved
	// value.
	Configured bool       `json:"configured"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
}
