package domain

import (
	"time"

	"github.com/google/uuid"
)

// OAuthProvider selects which 3-legged OAuth flow an oauth_states row
// belongs to -- see OAuthState.
type OAuthProvider string

const (
	OAuthProviderGDrive OAuthProvider = "gdrive"
	OAuthProviderSlack  OAuthProvider = "slack"
)

// OAuthState mirrors `oauth_states` (see
// db/migrations/0003_oauth_states.up.sql) -- a single-use CSRF token
// generated when an admin starts a "Connect to <provider>" flow and
// consumed exactly once on that provider's callback. Never marshaled to
// JSON directly; this is server-side bookkeeping only, the plaintext
// token itself (not this struct) is what round-trips through the
// provider's redirect as the `state` query param.
type OAuthState struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	UserID     uuid.UUID
	Provider   OAuthProvider
	TokenHash  string
	Metadata   map[string]string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}
