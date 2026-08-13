package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserAPIToken mirrors `user_api_tokens` -- a user's own bearer-token
// alternative to a JWT session, self-service (created/revoked from their
// own Profile page, not admin-managed like WebhookEndpoint). It carries no
// permissions of its own: middleware.JWTAuth resolves it back to its owning
// user and re-derives Claims from that user's current Role on every
// request, so it always reflects the user's live access, never a snapshot
// taken at creation time.
type UserAPIToken struct {
	ID         uuid.UUID `json:"id"`
	TenantID   uuid.UUID `json:"tenantId"`
	UserID     uuid.UUID `json:"userId"`
	Name       string    `json:"name"`
	TokenHash  string    `json:"-"`
	TokenLast4 string    `json:"tokenLast4"`
	// ExpiresAt is nil for a token the user explicitly opted out of
	// expiring; Create defaults it to now()+90d otherwise (see
	// service.UserAPITokenService).
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}
