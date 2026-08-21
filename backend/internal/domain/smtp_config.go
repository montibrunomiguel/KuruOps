package domain

import (
	"time"

	"github.com/google/uuid"
)

// SMTPConfig mirrors `tenant_smtp_config` (see
// db/migrations/0001_initial_schema.up.sql) -- same secret-reference
// pattern as domain.StorageConfig/domain.LDAPConfig: the actual SMTP
// password is never stored here, only an opaque reference into
// secrets.Store, tagged json:"-" so an admin's Settings GET can never leak
// it back out.
type SMTPConfig struct {
	TenantID          uuid.UUID `json:"tenantId"`
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	UseTLS            bool      `json:"useTls"`
	Username          string    `json:"username"`
	PasswordSecretRef string    `json:"-"`
	FromAddress       string    `json:"fromAddress"`
	FromName          *string   `json:"fromName,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}
