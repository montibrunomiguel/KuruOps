package domain

import (
	"time"

	"github.com/google/uuid"
)

// MFAPendingToken is the second, short-lived leg of a local login for a
// user with TOTP enrolled -- see db/migrations/0009_mfa_pending_tokens and
// service.AuthService.LoginLocal/VerifyMFA.
type MFAPendingToken struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	UserID     uuid.UUID
	TokenHash  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}
