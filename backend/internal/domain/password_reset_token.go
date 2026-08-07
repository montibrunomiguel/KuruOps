package domain

import (
	"time"

	"github.com/google/uuid"
)

// PasswordResetToken backs the self-service "forgot my password" flow --
// see service.PasswordResetService. Single-use (UsedAt), same
// hash-at-rest/rotate-on-use discipline as RefreshToken.
type PasswordResetToken struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
