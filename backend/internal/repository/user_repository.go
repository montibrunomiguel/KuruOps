package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type UserRepository struct{}

func NewUserRepository() *UserRepository {
	return &UserRepository{}
}

// userColumns joins roles so every domain.User comes back with Role already
// populated -- see domain.User's doc comment for why that's a join here,
// not a second lazy-loaded query.
const userColumns = `
	u.id, u.tenant_id, u.email, u.name, u.auth_provider, u.external_id, u.password_hash,
	u.role_id, u.mfa_totp_secret, u.is_active, u.must_change_password, u.last_login_at,
	u.created_at, u.updated_at,
	r.id, r.tenant_id, r.name, r.is_admin, r.resource_access, r.allowed_tags, r.created_at, r.updated_at`

const usersFrom = `from users u join roles r on r.id = u.role_id`

func (r *UserRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.User, error) {
	rows, err := tx.Query(ctx, `select `+userColumns+` `+usersFrom+` order by u.name asc`)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	users := []domain.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// ListSummaries backs the non-admin directory endpoint (see
// domain.UserSummary) -- active users only, since a deactivated user isn't
// a valid pick for an owner/assignee. No role join needed, this never
// exposes access info.
func (r *UserRepository) ListSummaries(ctx context.Context, tx pgx.Tx) ([]domain.UserSummary, error) {
	rows, err := tx.Query(ctx, `select id, name from users where is_active order by name asc`)
	if err != nil {
		return nil, fmt.Errorf("query user summaries: %w", err)
	}
	defer rows.Close()

	summaries := []domain.UserSummary{}
	for rows.Next() {
		var s domain.UserSummary
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			return nil, fmt.Errorf("scan user summary: %w", err)
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

func (r *UserRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.User, error) {
	row := tx.QueryRow(ctx, `select `+userColumns+` `+usersFrom+` where u.id = $1`, id)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// GetByEmail looks up a user within the already-tenant-scoped transaction
// (see the login flow in internal/httpserver/handlers/auth.go: the single
// default tenant is resolved first via TenantRepository.GetDefault, then
// this runs inside WithTenant).
func (r *UserRepository) GetByEmail(ctx context.Context, tx pgx.Tx, email string) (*domain.User, error) {
	row := tx.QueryRow(ctx, `select `+userColumns+` `+usersFrom+` where u.email = $1`, email)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// UpsertFederated creates or updates a user provisioned just-in-time from
// an LDAP/SAML login -- see AuthGroupMapping and the architecture review's
// "Auth: local + LDAP + SAML" section. auth_provider + external_id identify
// the user across logins; role_id is refreshed from the matching
// AuthGroupMapping on every login, so an IdP group change takes effect the
// next time the user signs in.
func (r *UserRepository) UpsertFederated(ctx context.Context, tx pgx.Tx, u *domain.User) error {
	row := tx.QueryRow(ctx, `
		insert into users (tenant_id, email, name, auth_provider, external_id, role_id)
		values ($1,$2,$3,$4,$5,$6)
		on conflict (tenant_id, email) do update set
			name = excluded.name,
			role_id = excluded.role_id,
			last_login_at = now(),
			updated_at = now()
		returning id, is_active, created_at, updated_at`,
		u.TenantID, u.Email, u.Name, u.AuthProvider, u.ExternalID, u.RoleID,
	)
	if err := row.Scan(&u.ID, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return fmt.Errorf("upsert federated user: %w", err)
	}
	return nil
}

// CreateLocal inserts a brand-new local-auth user (Settings -> Users &
// Roles -> "+ New User", admin-created). auth_provider is always "local"
// here -- LDAP/SAML users only ever get created via UpsertFederated on
// login. must_change_password is always true: the admin sets a one-time
// temp password (see UserService.CreateLocal), never the user's real one.
func (r *UserRepository) CreateLocal(ctx context.Context, tx pgx.Tx, u *domain.User, passwordHash string) error {
	row := tx.QueryRow(ctx, `
		insert into users (tenant_id, email, name, auth_provider, password_hash, role_id, must_change_password)
		values ($1,$2,$3,'local',$4,$5,true)
		returning id, is_active, must_change_password, created_at, updated_at`,
		u.TenantID, u.Email, u.Name, passwordHash, u.RoleID,
	)
	if err := row.Scan(&u.ID, &u.IsActive, &u.MustChangePassword, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return fmt.Errorf("insert local user: %w", err)
	}
	return nil
}

func (r *UserRepository) StampLastLogin(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `update users set last_login_at = now() where id = $1`, id)
	return err
}

func (r *UserRepository) UpdateAccess(ctx context.Context, tx pgx.Tx, id, roleID uuid.UUID) error {
	_, err := tx.Exec(ctx, `update users set role_id = $2, updated_at = now() where id = $1`, id, roleID)
	return err
}

// SetPassword rotates a local user's password and always clears
// must_change_password -- this is the only path that clears that flag, so
// there's one place that has to get it right (see
// service.AuthService.ChangePassword).
func (r *UserRepository) SetPassword(ctx context.Context, tx pgx.Tx, id uuid.UUID, passwordHash string) error {
	_, err := tx.Exec(ctx, `
		update users set password_hash = $2, must_change_password = false, updated_at = now()
		where id = $1`,
		id, passwordHash,
	)
	return err
}

// SetPasswordAndForceChange is the admin "reset a user's password" path --
// unlike SetPassword, it sets must_change_password back to true, since the
// new value is a one-time temp password the admin shares out of band (same
// as CreateLocal), not the user's own chosen password.
func (r *UserRepository) SetPasswordAndForceChange(ctx context.Context, tx pgx.Tx, id uuid.UUID, passwordHash string) error {
	_, err := tx.Exec(ctx, `
		update users set password_hash = $2, must_change_password = true, updated_at = now()
		where id = $1`,
		id, passwordHash,
	)
	return err
}

// UpdateProfile updates a local user's own name/email -- the self-service
// path, not the admin UpdateAccess path (role_id stays untouched here). See
// AuthService.UpdateProfile for the email-change password-confirmation
// guard that runs before this is ever called.
func (r *UserRepository) UpdateProfile(ctx context.Context, tx pgx.Tx, id uuid.UUID, name, email string) error {
	_, err := tx.Exec(ctx, `
		update users set name = $2, email = $3, updated_at = now()
		where id = $1`,
		id, name, email,
	)
	if err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	return nil
}

func (r *UserRepository) SetActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, active bool) error {
	_, err := tx.Exec(ctx, `update users set is_active = $2, updated_at = now() where id = $1`, id, active)
	return err
}

// --- LDAP/SAML group -> Role mapping (just-in-time provisioning) ---

const authGroupMappingColumns = `
	m.id, m.tenant_id, m.provider, m.external_group, m.role_id, m.created_at,
	r.id, r.tenant_id, r.name, r.is_admin, r.resource_access, r.allowed_tags, r.created_at, r.updated_at`

const authGroupMappingsFrom = `from auth_group_mappings m join roles r on r.id = m.role_id`

func (r *UserRepository) ListGroupMappings(ctx context.Context, tx pgx.Tx) ([]domain.AuthGroupMapping, error) {
	rows, err := tx.Query(ctx, `select `+authGroupMappingColumns+` `+authGroupMappingsFrom+` order by m.external_group asc`)
	if err != nil {
		return nil, fmt.Errorf("query auth group mappings: %w", err)
	}
	defer rows.Close()

	mappings := []domain.AuthGroupMapping{}
	for rows.Next() {
		m, err := scanAuthGroupMapping(rows)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, *m)
	}
	return mappings, rows.Err()
}

func (r *UserRepository) UpsertGroupMapping(ctx context.Context, tx pgx.Tx, m *domain.AuthGroupMapping) error {
	row := tx.QueryRow(ctx, `
		insert into auth_group_mappings (tenant_id, provider, external_group, role_id)
		values ($1,$2,$3,$4)
		on conflict (tenant_id, provider, external_group)
		do update set role_id = excluded.role_id
		returning id, created_at`,
		m.TenantID, m.Provider, m.ExternalGroup, m.RoleID,
	)
	if err := row.Scan(&m.ID, &m.CreatedAt); err != nil {
		return fmt.Errorf("upsert auth group mapping: %w", err)
	}
	return nil
}

func (r *UserRepository) DeleteGroupMapping(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from auth_group_mappings where id = $1`, id)
	return err
}

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	var role domain.Role
	err := row.Scan(
		&u.ID, &u.TenantID, &u.Email, &u.Name, &u.AuthProvider, &u.ExternalID, &u.PasswordHash,
		&u.RoleID, &u.MFATOTPSecret, &u.IsActive, &u.MustChangePassword, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt,
		&role.ID, &role.TenantID, &role.Name, &role.IsAdmin, &role.ResourceAccess, &role.AllowedTags, &role.CreatedAt, &role.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.Role = &role
	return &u, nil
}

func scanAuthGroupMapping(row pgx.Row) (*domain.AuthGroupMapping, error) {
	var m domain.AuthGroupMapping
	var role domain.Role
	err := row.Scan(
		&m.ID, &m.TenantID, &m.Provider, &m.ExternalGroup, &m.RoleID, &m.CreatedAt,
		&role.ID, &role.TenantID, &role.Name, &role.IsAdmin, &role.ResourceAccess, &role.AllowedTags, &role.CreatedAt, &role.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan auth group mapping: %w", err)
	}
	m.Role = &role
	return &m, nil
}
