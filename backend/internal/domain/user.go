package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ResourceAccess is a capability set, not a mutually-exclusive choice: a
// user can hold any combination of the three capabilities below, which is
// what lets a SOC analyst see Follow-up without full Incidents access while
// a CSIRT member holds all three. Stored as a Postgres text[] with a CHECK
// constraint (see db/migrations/0015_resource_access_capabilities.up.sql)
// rather than an enum array, so a future capability doesn't need another
// multi-step enum migration.
type ResourceAccess []string

const (
	ResourceCapabilityAlerts    = "alerts"
	ResourceCapabilityIncidents = "incidents"
	// ResourceCapabilityFollowup grants the Dashboard's Follow-up view
	// (SLA-breached incidents + escalated/investigating alerts) without
	// granting the general Incidents or Alerts sections -- see
	// middleware.RequireResourceAccess and the /api/v1/dashboard/followup
	// route, which is gated independently of /api/v1/incidents and
	// /api/v1/alerts.
	ResourceCapabilityFollowup = "followup"
)

// Has reports whether the set includes capability.
func (ra ResourceAccess) Has(capability string) bool {
	for _, c := range ra {
		if c == capability {
			return true
		}
	}
	return false
}

// ValidateResourceAccess rejects anything outside the three known
// capabilities before it reaches the database's CHECK constraint, so a bad
// value from Settings -> Users & Roles surfaces as a normal 400 instead of
// a raw Postgres constraint-violation message.
func ValidateResourceAccess(ra ResourceAccess) error {
	for _, c := range ra {
		switch c {
		case ResourceCapabilityAlerts, ResourceCapabilityIncidents, ResourceCapabilityFollowup:
		default:
			return fmt.Errorf("invalid resource access capability: %q", c)
		}
	}
	return nil
}

// User mirrors `users`. PasswordHash and MFATOTPSecret are tagged
// json:"-" so they can never leak through an API response even if a
// handler accidentally serializes the whole struct.
//
// Access (role tier, resource capabilities, tag scope) lives entirely on
// the referenced Role now, not on User directly -- see domain.Role's doc
// comment. Role is always populated by the repository (a join, not a lazy
// load) whenever a User is read, so callers can go straight to
// user.Role.IsAdmin / user.Role.ResourceAccess.Has(...) /
// user.Role.AllowedTags without a second query.
type User struct {
	ID            uuid.UUID    `json:"id"`
	TenantID      uuid.UUID    `json:"tenantId"`
	Email         string       `json:"email"`
	Name          string       `json:"name"`
	AuthProvider  AuthProvider `json:"authProvider"`
	ExternalID    *string      `json:"externalId,omitempty"`
	PasswordHash  *string      `json:"-"`
	RoleID        uuid.UUID    `json:"roleId"`
	Role          *Role        `json:"role"`
	MFATOTPSecret *string      `json:"-"`
	IsActive      bool         `json:"isActive"`
	// MustChangePassword locks the account to POST /api/v1/account/change-password
	// only (see middleware.RequirePasswordChanged) until the password is
	// rotated. Set true for the seeded default admin (0013_seed_default_admin.up.sql)
	// and never cleared except by a successful password change.
	MustChangePassword bool       `json:"mustChangePassword"`
	LastLoginAt        *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// UserSummary is the minimal, non-admin-safe projection of User -- id and
// display name only, no email/role/resourceAccess/etc. Backs GET
// /api/v1/users/directory (see UserHandlers.Directory), which any
// authenticated user can call to resolve a name for a userID they don't
// have permission to look up in full (e.g. domain.Incident.Assignees, or an
// incident_comments.author_id before that got denormalized) or to populate
// an assignee picker, without exposing the admin-only Settings -> Users list.
type UserSummary struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type AuthProvider string

const (
	AuthProviderLocal AuthProvider = "local"
	AuthProviderLDAP  AuthProvider = "ldap"
	AuthProviderSAML  AuthProvider = "saml"
)

// AuthGroupMapping mirrors `auth_group_mappings` — the LDAP group / SAML
// attribute value to Role mapping applied on every federated login
// (just-in-time provisioning), so IdP group membership changes take effect
// without an admin manually editing users. See architecture review, "Auth:
// local + LDAP + SAML".
type AuthGroupMapping struct {
	ID            uuid.UUID    `json:"id"`
	TenantID      uuid.UUID    `json:"tenantId"`
	Provider      AuthProvider `json:"provider"`
	ExternalGroup string       `json:"externalGroup"`
	RoleID        uuid.UUID    `json:"roleId"`
	Role          *Role        `json:"role"`
	CreatedAt     time.Time    `json:"createdAt"`
}
