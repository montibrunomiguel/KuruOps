package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/repository"
)

// UploadKeyService records which alert/incident each uploaded attachment
// key belongs to, so UploadHandlers' GET route can look the owning entity
// back up and reapply the allowedTags visibility check its POST route
// already enforces via resolveEntity -- see upload_keys' migration comment
// for why the object key itself can't be reverse-parsed for this.
type UploadKeyService struct {
	pool *db.Pool
	repo *repository.UploadKeyRepository
}

func NewUploadKeyService(pool *db.Pool, repo *repository.UploadKeyRepository) *UploadKeyService {
	return &UploadKeyService{pool: pool, repo: repo}
}

func (s *UploadKeyService) Record(ctx context.Context, tenantID uuid.UUID, key, contextType string, contextID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Insert(ctx, tx, tenantID, key, contextType, contextID)
	})
}

// Lookup returns the (contextType, contextID) key was uploaded against, and
// found=false if there's no row -- true for any key uploaded before this
// feature shipped, which callers should treat as "nothing to check
// against" rather than a hard failure.
func (s *UploadKeyService) Lookup(ctx context.Context, tenantID uuid.UUID, key string) (contextType string, contextID uuid.UUID, found bool, err error) {
	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var lookupErr error
		contextType, contextID, found, lookupErr = s.repo.Lookup(ctx, tx, tenantID, key)
		return lookupErr
	})
	return contextType, contextID, found, err
}
