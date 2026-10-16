package blobstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TenantRunner runs fn in a transaction scoped to one tenant (row-level security
// applies). *db.Pool satisfies it; the interface keeps this package free of a
// dependency on internal/db and lets a test substitute a fake.
type TenantRunner interface {
	WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error
}

// DatabaseStore keeps attachments in the attachment_blobs table (migration 0016), one
// store per tenant. It is the backend that lets the api run as more than one replica
// without a shared filesystem: every replica reads and writes the same rows.
//
// Content is held in memory for the duration of a Put or Get. That is fine for what it
// is used for -- the upload handler caps an attachment at 25 MB -- and a deployment
// that stores many or large attachments should use S3/GCS instead (see the migration).
type DatabaseStore struct {
	db       TenantRunner
	tenantID uuid.UUID
}

// NewDatabaseStore returns a Store for tenantID backed by the database.
func NewDatabaseStore(db TenantRunner, tenantID uuid.UUID) *DatabaseStore {
	return &DatabaseStore{db: db, tenantID: tenantID}
}

// Put stores content under key, replacing any object already there. size is advisory
// (the real length is what was read); an io.Reader that errors part way stores nothing.
func (s *DatabaseStore) Put(ctx context.Context, key string, content io.Reader, _ int64, contentType string) error {
	data, err := io.ReadAll(content)
	if err != nil {
		return fmt.Errorf("read attachment: %w", err)
	}
	return s.db.WithTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			insert into attachment_blobs (tenant_id, key, content_type, size_bytes, data)
			values ($1, $2, $3, $4, $5)
			on conflict (tenant_id, key) do update
			set content_type = excluded.content_type, size_bytes = excluded.size_bytes, data = excluded.data`,
			s.tenantID, key, contentType, int64(len(data)), data)
		return err
	})
}

// Get returns the object stored under key and the content type recorded at Put time,
// or ErrNotFound.
func (s *DatabaseStore) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	var (
		data        []byte
		contentType string
	)
	err := s.db.WithTenant(ctx, s.tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select data, content_type from attachment_blobs where tenant_id = $1 and key = $2`,
			s.tenantID, key).Scan(&data, &contentType)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(data)), contentType, nil
}

// FallbackStore writes to primary and reads from primary first, then from fallback
// when primary does not have the object. It exists for the move from local disk to
// the database: attachments uploaded before the switch are still on the old volume,
// and must stay readable instead of 404ing the moment the backend changes.
type FallbackStore struct {
	primary  Store
	fallback Store
}

// NewFallbackStore returns a Store that writes to primary and reads primary-then-fallback.
func NewFallbackStore(primary, fallback Store) *FallbackStore {
	return &FallbackStore{primary: primary, fallback: fallback}
}

func (s *FallbackStore) Put(ctx context.Context, key string, content io.Reader, size int64, contentType string) error {
	return s.primary.Put(ctx, key, content, size, contentType)
}

func (s *FallbackStore) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	rc, ct, err := s.primary.Get(ctx, key)
	if !errors.Is(err, ErrNotFound) {
		return rc, ct, err
	}
	return s.fallback.Get(ctx, key)
}
