package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type TagRepository struct{}

func NewTagRepository() *TagRepository {
	return &TagRepository{}
}

const tagColumns = `id, tenant_id, name, color, created_by, created_at`

func (r *TagRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.Tag, error) {
	rows, err := tx.Query(ctx, `select `+tagColumns+` from tags order by name asc`)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()

	tags := []domain.Tag{}
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		tags = append(tags, *t)
	}
	return tags, rows.Err()
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

func (r *TagRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from tags where id = $1`, id)
	return err
}

// FilterKnown returns the subset of names that exist in the tenant's tag
// catalog (case-insensitive, matching the unique index in
// 0016_tags_catalog.up.sql). Used by cmd/ingest to drop any webhook-supplied
// tag that hasn't been registered in Settings -> Tags, and by the
// Alert/Incident UpdateTags handlers to reject the same.
func (r *TagRepository) FilterKnown(ctx context.Context, tx pgx.Tx, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		select name from tags where lower(name) = any(select lower(unnest($1::text[])))`,
		names,
	)
	if err != nil {
		return nil, fmt.Errorf("filter known tags: %w", err)
	}
	defer rows.Close()

	known := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan tag name: %w", err)
		}
		known = append(known, name)
	}
	return known, rows.Err()
}

func scanTag(row pgx.Row) (*domain.Tag, error) {
	var t domain.Tag
	err := row.Scan(&t.ID, &t.TenantID, &t.Name, &t.Color, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan tag: %w", err)
	}
	return &t, nil
}
