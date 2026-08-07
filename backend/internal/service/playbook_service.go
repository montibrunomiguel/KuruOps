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

// genericPlaybookCategory is what MatchForAlertTitle falls back to when no
// playbook's keywords match — mirrors "General Security Event" in the
// design handoff. A tenant is expected to seed one playbook in this
// category; if none exists, MatchForAlertTitle simply returns nil.
const genericPlaybookCategory = "General Security Event"

type PlaybookService struct {
	pool *db.Pool
	repo *repository.PlaybookRepository
}

func NewPlaybookService(pool *db.Pool, repo *repository.PlaybookRepository) *PlaybookService {
	return &PlaybookService{pool: pool, repo: repo}
}

func (s *PlaybookService) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.Playbook, error) {
	var pb *domain.Playbook
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.Get(ctx, tx, id)
		pb = v
		return err
	})
	return pb, err
}

func (s *PlaybookService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.Playbook, error) {
	var playbooks []domain.Playbook
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		playbooks = v
		return err
	})
	return playbooks, err
}

func (s *PlaybookService) Create(ctx context.Context, tenantID, actorID uuid.UUID, in domain.SavePlaybookInput) (*domain.Playbook, error) {
	pb := &domain.Playbook{
		TenantID:    tenantID,
		Title:       in.Title,
		Category:    in.Category,
		Description: in.Description,
		Keywords:    orEmptySlice(in.Keywords), // playbooks.keywords is NOT NULL
		Steps:       in.Steps,
		CreatedBy:   &actorID,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, pb)
	})
	if err != nil {
		return nil, fmt.Errorf("create playbook: %w", err)
	}
	return pb, nil
}

func (s *PlaybookService) Update(ctx context.Context, tenantID, id uuid.UUID, in domain.SavePlaybookInput) (*domain.Playbook, error) {
	pb := &domain.Playbook{
		ID:          id,
		TenantID:    tenantID,
		Title:       in.Title,
		Category:    in.Category,
		Description: in.Description,
		Keywords:    orEmptySlice(in.Keywords), // playbooks.keywords is NOT NULL
		Steps:       in.Steps,
	}
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Update(ctx, tx, pb)
	})
	if err != nil {
		return nil, fmt.Errorf("update playbook: %w", err)
	}
	return pb, nil
}

func (s *PlaybookService) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx, id)
	})
}

// MatchForAlertTitle picks the "Related Playbook" shown on Alert Detail:
// the first playbook whose keywords substring-match alertTitle, falling
// back to a playbook in the genericPlaybookCategory bucket, or nil if the
// tenant hasn't set one up. Matching is done in Go after loading the (small)
// playbook library rather than in SQL, since "keyword is a substring of the
// title, case-insensitive" isn't expressible as plain array containment.
func (s *PlaybookService) MatchForAlertTitle(ctx context.Context, tenantID uuid.UUID, alertTitle string) (*domain.Playbook, error) {
	playbooks, err := s.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	var fallback *domain.Playbook
	for i := range playbooks {
		pb := playbooks[i]
		if pb.MatchesTitle(alertTitle) {
			return &pb, nil
		}
		if fallback == nil && pb.Category == genericPlaybookCategory {
			fallback = &pb
		}
	}
	return fallback, nil
}
