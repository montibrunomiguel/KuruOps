package domain

import (
	"time"

	"github.com/google/uuid"
)

// PersonalAccessToken lets a user call the API programmatically (scripts,
// integrations) from Settings -> My Account, without a 15-minute session
// JWT. Unlike a JWT's claims (a snapshot taken at login, stale until the
// token's next refresh), a PAT is looked up by hash on every request (see
// middleware's PAT auth path), so it always reflects the holder's current
// Role, not a stale one.
type PersonalAccessToken struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenantId"`
	UserID     uuid.UUID  `json:"userId"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	TokenLast4 string     `json:"tokenLast4"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}
