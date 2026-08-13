package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type FieldMappingTemplateRepository struct{}

func NewFieldMappingTemplateRepository() *FieldMappingTemplateRepository {
	return &FieldMappingTemplateRepository{}
}

const fieldMappingTemplateColumns = `id, tenant_id, name, rules, created_by, created_at, updated_at`

func (r *FieldMappingTemplateRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.FieldMappingTemplate, error) {
	rows, err := tx.Query(ctx, `select `+fieldMappingTemplateColumns+` from field_mapping_templates order by name asc`)
	if err != nil {
		return nil, fmt.Errorf("query field mapping templates: %w", err)
	}
	defer rows.Close()

	templates := []domain.FieldMappingTemplate{}
	for rows.Next() {
		t, err := scanFieldMappingTemplate(rows)
		if err != nil {
			return nil, err
		}
		templates = append(templates, *t)
	}
	return templates, rows.Err()
}

func (r *FieldMappingTemplateRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.FieldMappingTemplate, error) {
	row := tx.QueryRow(ctx, `select `+fieldMappingTemplateColumns+` from field_mapping_templates where id = $1`, id)
	return scanFieldMappingTemplate(row)
}

func (r *FieldMappingTemplateRepository) Insert(ctx context.Context, tx pgx.Tx, t *domain.FieldMappingTemplate) error {
	rules, err := json.Marshal(t.Rules)
	if err != nil {
		return fmt.Errorf("marshal field mapping rules: %w", err)
	}
	row := tx.QueryRow(ctx, `
		insert into field_mapping_templates (tenant_id, name, rules, created_by)
		values ($1,$2,$3,$4)
		returning id, created_at, updated_at`,
		t.TenantID, t.Name, rules, t.CreatedBy,
	)
	if err := row.Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return fmt.Errorf("insert field mapping template: %w", err)
	}
	return nil
}

func (r *FieldMappingTemplateRepository) Update(ctx context.Context, tx pgx.Tx, t *domain.FieldMappingTemplate) error {
	rules, err := json.Marshal(t.Rules)
	if err != nil {
		return fmt.Errorf("marshal field mapping rules: %w", err)
	}
	_, err = tx.Exec(ctx, `
		update field_mapping_templates
		set name = $2, rules = $3, updated_at = now()
		where id = $1`,
		t.ID, t.Name, rules,
	)
	if err != nil {
		return fmt.Errorf("update field mapping template: %w", err)
	}
	return nil
}

func (r *FieldMappingTemplateRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from field_mapping_templates where id = $1`, id)
	return err
}

func scanFieldMappingTemplate(row pgx.Row) (*domain.FieldMappingTemplate, error) {
	var t domain.FieldMappingTemplate
	var rules []byte
	err := row.Scan(&t.ID, &t.TenantID, &t.Name, &rules, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan field mapping template: %w", err)
	}
	t.Rules = []domain.FieldMappingRule{}
	if len(rules) > 0 {
		if err := json.Unmarshal(rules, &t.Rules); err != nil {
			return nil, fmt.Errorf("unmarshal field mapping rules: %w", err)
		}
	}
	return &t, nil
}
