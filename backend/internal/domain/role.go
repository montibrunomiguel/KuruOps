package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role mirrors `roles` -- Settings -> Roles. Replaces the old three loose
// per-user fields (role tier, resource_access, allowed_tags) with one
// reusable, nameable entity: build "SOC L1" or "Analyst - Acme" once,
// assign it to any number of users, instead of re-picking the same three
// settings on every user individually. IsAdmin is what gates
// Settings access (see middleware.RequireAdmin) -- a plain boolean rather
// than a role-tier enum, since nothing in this codebase ever branches on a
// tier beyond "is this an admin or not"; every other access decision
// already goes through ResourceAccess/AllowedTags.
type Role struct {
	ID       uuid.UUID `json:"id"`
	TenantID uuid.UUID `json:"tenantId"`
	Name     string    `json:"name"`
	IsAdmin  bool      `json:"isAdmin"`
	// ResourceAccess and AllowedTags have the exact same semantics they had
	// as domain.User fields before this migration -- see ResourceAccess's
	// and User.AllowedTags' doc comments.
	ResourceAccess ResourceAccess `json:"resourceAccess"`
	AllowedTags    []string       `json:"allowedTags"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

// SaveRoleInput is what Settings -> Roles creates/edits.
type SaveRoleInput struct {
	Name           string
	IsAdmin        bool
	ResourceAccess ResourceAccess
	AllowedTags    []string
}
