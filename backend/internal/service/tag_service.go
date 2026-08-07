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

type TagService struct {
	pool *db.Pool
	repo *repository.TagRepository
}

func NewTagService(pool *db.Pool, repo *repository.TagRepository) *TagService {
	return &TagService{pool: pool, repo: repo}
}

func (s *TagService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Tag, error) {
	var tags []domain.Tag
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		tags = v
		return err
	})
	return tags, err
}

func (s *TagService) Create(ctx context.Context, tenantID, actorID uuid.UUID, name string, color *string) (*domain.Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("tag name is required")
	}
	t := &domain.Tag{TenantID: tenantID, Name: name, Color: color, CreatedBy: &actorID}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Create(ctx, tx, t)
	})
	if err != nil {
		return nil, fmt.Errorf("create tag: %w", err)
	}
	return t, nil
}

func (s *TagService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
}

// FilterKnown drops any tag that isn't in the tenant's catalog -- see
// TagRepository.FilterKnown. Used wherever a caller (webhook ingest, an
// alert/incident tag edit) supplies tag names that need validating against
// Settings -> Tags rather than trusted verbatim.
func (s *TagService) FilterKnown(ctx context.Context, tenantID uuid.UUID, names []string) ([]string, error) {
	var known []string
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.FilterKnown(ctx, tx, names)
		known = v
		return err
	})
	return known, err
}
