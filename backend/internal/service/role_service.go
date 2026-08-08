package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

// unmappedFallbackRoleName is the role a federated (LDAP/SAML) user gets
// when their IdP groups match no configured AuthGroupMapping -- get-or-
// created lazily per tenant (see EnsureUnmappedFallback) since, unlike
// before this migration, a user's access can no longer be a bespoke,
// un-persisted combination; it always has to be a real Role row.
const unmappedFallbackRoleName = "Unmapped"

// unmappedGroupSentinel is a tag no real alert/incident will ever carry.
// AllowedTags treats an EMPTY slice as "unrestricted, sees everything" (see
// the design handoff's tag model) -- so an unmapped federated user can't be
// given an empty slice to mean "no access", that would mean the opposite.
// This sentinel is the least-privilege default until an admin adds a real
// AuthGroupMapping for the user's IdP group.
const unmappedGroupSentinel = "__unmapped__"

// RoleService is Settings -> Roles: named, reusable bundles of admin
// access + resource capabilities + tag scope, assigned to users and to
// LDAP/SAML group mappings. See domain.Role's doc comment for why this
// replaced the old three loose per-user fields.
type RoleService struct {
	pool  *db.Pool
	roles *repository.RoleRepository
}

func NewRoleService(pool *db.Pool, roles *repository.RoleRepository) *RoleService {
	return &RoleService{pool: pool, roles: roles}
}

func (s *RoleService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Role, error) {
	var roles []domain.Role
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.roles.List(ctx, tx)
		roles = v
		return err
	})
	return roles, err
}

func (s *RoleService) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.Role, error) {
	var role *domain.Role
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.roles.Get(ctx, tx, id)
		role = v
		return err
	})
	return role, err
}

func (s *RoleService) Create(ctx context.Context, tenantID uuid.UUID, in domain.SaveRoleInput) (*domain.Role, error) {
	if err := validateRoleInput(in); err != nil {
		return nil, err
	}
	role := &domain.Role{
		TenantID: tenantID, Name: in.Name, IsAdmin: in.IsAdmin,
		ResourceAccess: in.ResourceAccess, AllowedTags: orEmptySlice(in.AllowedTags),
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.roles.Create(ctx, tx, role)
	})
	if err != nil {
		return nil, fmt.Errorf("create role: %w", err)
	}
	return role, nil
}

func (s *RoleService) Update(ctx context.Context, tenantID, id uuid.UUID, in domain.SaveRoleInput) (*domain.Role, error) {
	if err := validateRoleInput(in); err != nil {
		return nil, err
	}
	role := &domain.Role{
		ID: id, TenantID: tenantID, Name: in.Name, IsAdmin: in.IsAdmin,
		ResourceAccess: in.ResourceAccess, AllowedTags: orEmptySlice(in.AllowedTags),
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.roles.Update(ctx, tx, role)
	})
	if err != nil {
		return nil, fmt.Errorf("update role: %w", err)
	}
	return role, nil
}

// Delete refuses to remove a role still assigned to any user -- every user
// row requires a non-null role_id, so deleting an in-use role would either
// violate that FK or (worse, if it didn't) leave a user with no access
// model at all.
func (s *RoleService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		count, err := s.roles.CountUsers(ctx, tx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return fmt.Errorf("role is assigned to %d user(s), reassign them before deleting", count)
		}
		return s.roles.Delete(ctx, tx, id)
	})
}

// EnsureUnmappedFallback returns the tenant's fallback role for a federated
// user whose IdP groups matched no AuthGroupMapping, creating it on first
// use. Must be called from within an already-open tenant-scoped
// transaction (see AuthService.ProvisionFederated).
func (s *RoleService) EnsureUnmappedFallback(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (uuid.UUID, error) {
	existing, err := s.roles.GetByName(ctx, tx, unmappedFallbackRoleName)
	if err != nil {
		return uuid.Nil, fmt.Errorf("load fallback role: %w", err)
	}
	if existing != nil {
		return existing.ID, nil
	}
	role := &domain.Role{
		TenantID: tenantID, Name: unmappedFallbackRoleName, IsAdmin: false,
		ResourceAccess: domain.ResourceAccess{domain.ResourceCapabilityAlerts, domain.ResourceCapabilityIncidents},
		AllowedTags:    []string{unmappedGroupSentinel},
	}
	if err := s.roles.Create(ctx, tx, role); err != nil {
		return uuid.Nil, fmt.Errorf("create fallback role: %w", err)
	}
	return role.ID, nil
}

func validateRoleInput(in domain.SaveRoleInput) error {
	if in.Name == "" {
		return fmt.Errorf("name is required")
	}
	return domain.ValidateResourceAccess(in.ResourceAccess)
}
