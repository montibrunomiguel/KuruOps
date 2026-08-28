package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

type TagRepository struct{}

func NewTagRepository() *TagRepository {
	return &TagRepository{}
}

const tagColumns = `id, tenant_id, name, color, created_by, created_at`

func (r *TagRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.Tag, error) {
	return queryList(ctx, tx, `select `+tagColumns+` from tags order by name asc`, scanTag)
}

func (r *TagRepository) Create(ctx context.Context, tx pgx.Tx, t *domain.Tag) error {
	row := tx.QueryRow(ctx, `
		insert into tags (tenant_id, name, color, created_by)
		values ($1,$2,$3,$4)
		returning id, created_at`,
		t.TenantID, t.Name, t.Color, t.CreatedBy,
	)
	if err := row.Scan(&t.ID, &t.CreatedAt); err != nil {
		return fmt.Errorf("insert tag: %w", err)
	}
	return nil
}

// Delete returns the deleted tag's name (empty if id didn't match any row)
// so callers can record a meaningful audit diff without a separate fetch.
func (r *TagRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) (string, error) {
	var name string
	err := tx.QueryRow(ctx, `delete from tags where id = $1 returning name`, id).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return name, nil
}

// FilterKnown returns the subset of names that exist in the tenant's tag
// catalog (case-insensitive, matching the unique index in
// db/migrations/0001_initial_schema.up.sql). Used by cmd/ingest to drop any webhook-supplied
// tag that hasn't been registered in Settings -> Tags, and by the
// Alert/Incident UpdateTags handlers to reject the same.
func (r *TagRepository) FilterKnown(ctx context.Context, tx pgx.Tx, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	return queryList(ctx, tx, `
		select name from tags where lower(name) = any(select lower(unnest($1::text[])))`,
		func(row pgx.Row) (*string, error) {
			var name string
			if err := row.Scan(&name); err != nil {
				return nil, fmt.Errorf("scan tag name: %w", err)
			}
			return &name, nil
		},
		names,
	)
}

// EnsureExist inserts any name in names that isn't already in the tenant's
// tag catalog (case-insensitively, same matching as FilterKnown), leaving
// existing rows untouched. "on conflict ... do nothing" targets the exact
// expression the tags_tenant_name_uq unique index is built on -- safe even
// if names contains duplicates, or if a concurrent insert (another request
// racing to create the same new tag) beats this one to it.
func (r *TagRepository) EnsureExist(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, names []string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		insert into tags (tenant_id, name)
		select $1, unnest($2::text[])
		on conflict (tenant_id, lower(name)) do nothing`,
		tenantID, names,
	)
	if err != nil {
		return fmt.Errorf("ensure tags exist: %w", err)
	}
	return nil
}

func scanTag(row pgx.Row) (*domain.Tag, error) {
	var t domain.Tag
	err := row.Scan(&t.ID, &t.TenantID, &t.Name, &t.Color, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan tag: %w", err)
	}
	return &t, nil
}
