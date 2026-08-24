package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
)

type TagService struct {
	pool  *db.Pool
	repo  *repository.TagRepository
	audit *repository.AdminAuditEventRepository
}

func NewTagService(pool *db.Pool, repo *repository.TagRepository, audit *repository.AdminAuditEventRepository) *TagService {
	return &TagService{pool: pool, repo: repo, audit: audit}
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
		if err := s.repo.Create(ctx, tx, t); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": nil, "to": map[string]any{"name": t.Name, "color": t.Color}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "tags", Action: "create", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a tag named %q already exists", name)
		}
		return nil, fmt.Errorf("create tag: %w", err)
	}
	return t, nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505) -- used to translate a raw constraint error
// (e.g. tags_tenant_name_uq) into a message a user can act on, instead of
// letting the driver's own wording reach the API response.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (s *TagService) Delete(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		name, err := s.repo.Delete(ctx, tx, id)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": map[string]any{"id": id, "name": name}, "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "tags", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// FilterKnown drops any tag that isn't in the tenant's catalog -- see
// TagRepository.FilterKnown. Used wherever a caller (an alert/incident tag
// edit) supplies tag names that need validating against Settings -> Tags
// rather than trusted verbatim -- an analyst can only attach a tag an admin
// has already curated there. See EnsureExist for cmd/ingest's own use,
// which has no such curation step available to it.
func (s *TagService) FilterKnown(ctx context.Context, tenantID uuid.UUID, names []string) ([]string, error) {
	var known []string
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.FilterKnown(ctx, tx, names)
		known = v
		return err
	})
	return known, err
}

// EnsureExist makes sure every name in names exists in the tenant's tag
// catalog, creating (no color, no CreatedBy -- these are system-created
// from an inbound webhook payload, not an admin's own Settings -> Tags
// action) whichever ones don't, then returns the full set using each tag's
// canonical stored casing. Unlike FilterKnown, nothing here is ever
// dropped: a webhook sender has no Settings UI to pre-register a tag in
// before sending it, so requiring that up front (the rule an analyst's own
// manual tag edit is still held to) would just silently lose legitimate
// tags. A newly-created tag shows up in Settings -> Tags immediately,
// manageable (color, rename via delete+recreate, delete) the same as any
// other -- and, until an admin adds it to a restricted role's AllowedTags
// (see domain.Role.AllowedTags), stays invisible to analysts scoped away
// from it, same as any other tag: creation alone never grants visibility.
func (s *TagService) EnsureExist(ctx context.Context, tenantID uuid.UUID, names []string) ([]string, error) {
	cleaned := cleanTagNames(names)
	if len(cleaned) == 0 {
		return nil, nil
	}
	var result []string
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.EnsureExist(ctx, tx, tenantID, cleaned); err != nil {
			return err
		}
		v, err := s.repo.FilterKnown(ctx, tx, cleaned)
		result = v
		return err
	})
	return result, err
}

// cleanTagNames trims whitespace (matching Create's own normalization) and
// drops anything left blank -- a webhook sender's tag list is untrusted
// input, unlike Create's caller (an admin typing into a form field that
// already has its own required-field validation).
func cleanTagNames(names []string) []string {
	cleaned := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			cleaned = append(cleaned, n)
		}
	}
	return cleaned
}
