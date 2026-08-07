package domain

import (
	"time"

	"github.com/google/uuid"
)

// RefreshToken lets a client exchange a long-lived credential for a fresh
// 15-minute access JWT (see internal/authn/jwt.go) without re-entering a
// password, while giving the server an actual revocation point access
// tokens alone don't have -- see AuthService.RevokeSessions.
type RefreshToken struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}
