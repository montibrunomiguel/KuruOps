package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

type FieldMappingTemplateService struct {
	pool *db.Pool
	repo *repository.FieldMappingTemplateRepository
}

func NewFieldMappingTemplateService(pool *db.Pool, repo *repository.FieldMappingTemplateRepository) *FieldMappingTemplateService {
	return &FieldMappingTemplateService{pool: pool, repo: repo}
}

func (s *FieldMappingTemplateService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.FieldMappingTemplate, error) {
	var templates []domain.FieldMappingTemplate
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		templates = v
		return err
	})
	return templates, err
}

func (s *FieldMappingTemplateService) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.FieldMappingTemplate, error) {
	var t *domain.FieldMappingTemplate
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.Get(ctx, tx, id)
		t = v
		return err
	})
	return t, err
}

func (s *FieldMappingTemplateService) Create(ctx context.Context, tenantID, actorID uuid.UUID, name string, rules []domain.FieldMappingRule) (*domain.FieldMappingTemplate, error) {
	name, rules, err := validateFieldMappingTemplate(name, rules)
	if err != nil {
		return nil, err
	}
	t := &domain.FieldMappingTemplate{TenantID: tenantID, Name: name, Rules: rules, CreatedBy: &actorID}
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, t)
	})
	if err != nil {
		return nil, fmt.Errorf("create field mapping template: %w", err)
	}
	return t, nil
}

func (s *FieldMappingTemplateService) Update(ctx context.Context, tenantID, id uuid.UUID, name string, rules []domain.FieldMappingRule) (*domain.FieldMappingTemplate, error) {
	name, rules, err := validateFieldMappingTemplate(name, rules)
	if err != nil {
		return nil, err
	}
	t := &domain.FieldMappingTemplate{ID: id, TenantID: tenantID, Name: name, Rules: rules}
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Update(ctx, tx, t)
	})
	if err != nil {
		return nil, fmt.Errorf("update field mapping template: %w", err)
	}
	return t, nil
}

func (s *FieldMappingTemplateService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
}

// validateFieldMappingTemplate trims and rejects a blank name, and drops
// any rule with an empty JSONPath or Label rather than persisting a rule
// that could never match anything or would render a blank field label --
// same "don't fail the whole request over one bad-looking optional piece"
// spirit as TagService.FilterKnown, but for a request the caller (an admin
// filling out a form) directly controls, silently dropping is preferable to
// a round-trip error over a field they'll immediately notice is missing.
func validateFieldMappingTemplate(name string, rules []domain.FieldMappingRule) (string, []domain.FieldMappingRule, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, fmt.Errorf("template name is required")
	}
	cleaned := make([]domain.FieldMappingRule, 0, len(rules))
	for _, r := range rules {
		path := strings.TrimSpace(r.JSONPath)
		label := strings.TrimSpace(r.Label)
		if path == "" || label == "" {
			continue
		}
		cleaned = append(cleaned, domain.FieldMappingRule{JSONPath: path, Label: label})
	}
	return name, cleaned, nil
}
