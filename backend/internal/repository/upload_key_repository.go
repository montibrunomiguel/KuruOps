package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UploadKeyRepository backs upload_keys -- see that migration's own comment
// for why an uploaded attachment's storage key can't just be reverse-parsed
// to find the alert/incident it belongs to.
type UploadKeyRepository struct{}

func NewUploadKeyRepository() *UploadKeyRepository {
	return &UploadKeyRepository{}
}

func (r *UploadKeyRepository) Insert(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key, contextType string, contextID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		insert into upload_keys (key, tenant_id, context_type, context_id)
		values ($1, $2, $3, $4)`,
		key, tenantID, contextType, contextID,
	)
	if err != nil {
		return fmt.Errorf("insert upload key: %w", err)
	}
	return nil
}

// Lookup returns the (contextType, contextID) key was uploaded against, and
// found=false if key has no row -- true for every key uploaded before this
// table existed, which callers should treat as "nothing to check against"
// rather than a hard failure (see UploadHandlers.checkTagAccess).
func (r *UploadKeyRepository) Lookup(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, key string) (contextType string, contextID uuid.UUID, found bool, err error) {
	row := tx.QueryRow(ctx, `select context_type, context_id from upload_keys where tenant_id = $1 and key = $2`, tenantID, key)
	err = row.Scan(&contextType, &contextID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", uuid.Nil, false, nil
	}
	if err != nil {
		return "", uuid.Nil, false, fmt.Errorf("lookup upload key: %w", err)
	}
	return contextType, contextID, true, nil
}
