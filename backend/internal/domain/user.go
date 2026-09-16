package domain

import (
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// ResourceAccess is a capability set, not a mutually-exclusive choice: a
// user can hold any combination of the three capabilities below, which is
// what lets a SOC analyst see Follow-up without full Incidents access while
// a CSIRT member holds all three. Stored as a Postgres text[] with a CHECK
// constraint (see db/migrations/0001_initial_schema.up.sql)
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

// phonePattern is E.164-like: a leading '+', then 7-15 digits, first digit
// non-zero (a bare national number with no country code, e.g. "5511...",
// is rejected -- the whole point is Escala de Acionamento's webhook
// payload needs a dialable, unambiguous number).
var phonePattern = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// ValidatePhone rejects a non-empty phone that doesn't include a country
// code in E.164-ish shape. Called wherever User.Phone is written
// (UserService.CreateLocal/UpdatePhone, AuthService.UpdateProfile) --
// empty stays valid, since phone remains optional.
func ValidatePhone(phone string) error {
	if phone == "" {
		return nil
	}
	if !phonePattern.MatchString(phone) {
		return fmt.Errorf("phone must include a country code, e.g. +5511912345678")
	}
	return nil
}

// ValidateEmail rejects an address that isn't shaped like one.
//
// Deliberately permissive -- one "@", something either side, a dot in the
// domain, no whitespace. The goal is not RFC 5322 conformance (that
// grammar accepts addresses no mail server would route, and rejecting
// valid-but-unusual real addresses is a worse failure than letting an odd
// one through); it is to catch the typo that produces an account nobody
// can ever sign in to or reach.
//
// This matters because the email IS the login identifier and the only
// channel for a password reset: "not-an-email", "a@" and "@b.com" all
// created perfectly valid-looking accounts before this existed, and the
// mistake only surfaced when the person never received their credentials.
func ValidateEmail(email string) error {
	if !emailPattern.MatchString(email) {
		return fmt.Errorf("%q is not a valid email address", email)
	}
	return nil
}

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s.]+(\.[^@\s.]+)+$`)

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
	// rotated. Set true for the seeded default admin (db/migrations/0002_seed_default_admin.up.sql)
	// and never cleared except by a successful password change.
	MustChangePassword bool `json:"mustChangePassword"`
	// Phone is optional but, when set, must pass ValidatePhone (E.164-ish,
	// country code required) -- surfaced in Escala de Acionamento's webhook
	// payload placeholders as {{analystPhone}} once a step resolves who's on
	// call, where an ambiguous number without a country code isn't useful.
	Phone       *string    `json:"phone,omitempty"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
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
