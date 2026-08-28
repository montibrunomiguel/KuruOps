package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

type RoleRepository struct{}

func NewRoleRepository() *RoleRepository {
	return &RoleRepository{}
}

const roleColumns = `id, tenant_id, name, is_admin, resource_access, allowed_tags, created_at, updated_at`

func (r *RoleRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.Role, error) {
	return queryList(ctx, tx, `select `+roleColumns+` from roles order by name asc`, scanRole)
}

func (r *RoleRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Role, error) {
	return queryOne(ctx, tx, `select `+roleColumns+` from roles where id = $1`, scanRole, id)
}

// GetByName is used by EnsureUnmappedFallback (get-or-create semantics) --
// see RoleService for why a fallback role can't just be synthesized ad hoc
// anymore now that every user must reference a real Role row.
func (r *RoleRepository) GetByName(ctx context.Context, tx pgx.Tx, name string) (*domain.Role, error) {
	return queryOne(ctx, tx, `select `+roleColumns+` from roles where name = $1`, scanRole, name)
}

func (r *RoleRepository) Create(ctx context.Context, tx pgx.Tx, role *domain.Role) error {
	row := tx.QueryRow(ctx, `
		insert into roles (tenant_id, name, is_admin, resource_access, allowed_tags)
		values ($1,$2,$3,$4,$5)
		returning id, created_at, updated_at`,
		role.TenantID, role.Name, role.IsAdmin, role.ResourceAccess, role.AllowedTags,
	)
	if err := row.Scan(&role.ID, &role.CreatedAt, &role.UpdatedAt); err != nil {
		return fmt.Errorf("insert role: %w", err)
	}
	return nil
}

func (r *RoleRepository) Update(ctx context.Context, tx pgx.Tx, role *domain.Role) error {
	row := tx.QueryRow(ctx, `
		update roles set name = $2, is_admin = $3, resource_access = $4, allowed_tags = $5, updated_at = now()
		where id = $1
		returning updated_at`,
		role.ID, role.Name, role.IsAdmin, role.ResourceAccess, role.AllowedTags,
	)
	if err := row.Scan(&role.UpdatedAt); err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	return nil
}

func (r *RoleRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from roles where id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	return nil
}

// CountUsers returns how many users currently reference roleID -- used to
// block deleting a role that's still assigned to someone (see
// RoleService.Delete), same "can't delete something in use" discipline as
// TagRepository's tag-in-use check.
func (r *RoleRepository) CountUsers(ctx context.Context, tx pgx.Tx, roleID uuid.UUID) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `select count(*) from users where role_id = $1`, roleID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count users for role: %w", err)
	}
	return count, nil
}

func scanRole(row pgx.Row) (*domain.Role, error) {
	var role domain.Role
	err := row.Scan(
		&role.ID, &role.TenantID, &role.Name, &role.IsAdmin,
		&role.ResourceAccess, &role.AllowedTags, &role.CreatedAt, &role.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan role: %w", err)
	}
	return &role, nil
}
