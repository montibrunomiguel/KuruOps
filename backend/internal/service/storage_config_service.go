package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/blobstore"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

// StorageConfigService is Settings -> Storage Integration: lets an admin
// point alert/incident evidence uploads at an S3 or GCS bucket instead of
// the API container's local disk (see handlers.UploadHandlers). Mirrors
// IdentityConfigService's LDAP/SAML shape -- same "empty string on save
// means keep the existing secret" convention.
type StorageConfigService struct {
	pool      *db.Pool
	repo      *repository.StorageConfigRepository
	secrets   secrets.Store
	uploadDir string
}

func NewStorageConfigService(pool *db.Pool, repo *repository.StorageConfigRepository, store secrets.Store, uploadDir string) *StorageConfigService {
	return &StorageConfigService{pool: pool, repo: repo, secrets: store, uploadDir: uploadDir}
}

func (s *StorageConfigService) Get(ctx context.Context, tenantID uuid.UUID) (*domain.StorageConfig, error) {
	var cfg *domain.StorageConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.repo.Get(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}

func (s *StorageConfigService) Delete(ctx context.Context, tenantID uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.repo.Delete(ctx, tx)
	})
}

type SaveS3Input struct {
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string // plaintext; "" on update means keep existing
}

func (s *StorageConfigService) SaveS3(ctx context.Context, tenantID uuid.UUID, in SaveS3Input) error {
	if in.Bucket == "" || in.Region == "" || in.AccessKeyID == "" {
		return fmt.Errorf("bucket, region, and accessKeyId are required")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		ref, err := s.resolveSecretRef(ctx, tx, tenantID, in.SecretAccessKey, "storage-s3-secret-access-key", func(existing *domain.StorageConfig) string {
			if existing != nil && existing.Provider == domain.StorageProviderS3 {
				return existing.S3SecretAccessKeySecretRef
			}
			return ""
		})
		if err != nil {
			return err
		}
		return s.repo.Upsert(ctx, tx, &domain.StorageConfig{
			TenantID: tenantID, Provider: domain.StorageProviderS3,
			S3Bucket: &in.Bucket, S3Region: &in.Region, S3AccessKeyID: &in.AccessKeyID,
			S3SecretAccessKeySecretRef: ref,
		})
	})
}

type SaveGCSInput struct {
	Bucket          string
	ProjectID       string
	CredentialsJSON string // plaintext service account key JSON; "" on update means keep existing
}

func (s *StorageConfigService) SaveGCS(ctx context.Context, tenantID uuid.UUID, in SaveGCSInput) error {
	if in.Bucket == "" || in.ProjectID == "" {
		return fmt.Errorf("bucket and projectId are required")
	}
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		ref, err := s.resolveSecretRef(ctx, tx, tenantID, in.CredentialsJSON, "storage-gcs-credentials-json", func(existing *domain.StorageConfig) string {
			if existing != nil && existing.Provider == domain.StorageProviderGCS {
				return existing.GCSCredentialsJSONSecretRef
			}
			return ""
		})
		if err != nil {
			return err
		}
		return s.repo.Upsert(ctx, tx, &domain.StorageConfig{
			TenantID: tenantID, Provider: domain.StorageProviderGCS,
			GCSBucket: &in.Bucket, GCSProjectID: &in.ProjectID,
			GCSCredentialsJSONSecretRef: ref,
		})
	})
}

// resolveSecretRef is SaveS3/SaveGCS's use of secrets.PutOrKeepExisting: a
// new value is always stored fresh; a blank one falls back to whatever the
// same provider already had. Unlike SMTP, the very first save for a
// provider requires a real value -- there's nothing to fall back to yet,
// and an empty storage credential isn't a valid configuration the way an
// unauthenticated SMTP relay is.
func (s *StorageConfigService) resolveSecretRef(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, plaintext, purpose string, existingRef func(*domain.StorageConfig) string) (string, error) {
	existing, err := s.repo.Get(ctx, tx)
	if err != nil {
		return "", fmt.Errorf("load existing storage config: %w", err)
	}
	ref, err := secrets.PutOrKeepExisting(ctx, s.secrets, tenantID.String(), purpose, plaintext, existingRef(existing))
	if err != nil {
		return "", err
	}
	if ref == "" {
		return "", fmt.Errorf("a credential value is required for initial configuration")
	}
	return ref, nil
}

// BuildStore resolves the blobstore.Store a caller should upload evidence
// to right now: the tenant's configured S3/GCS bucket if one exists, local
// disk otherwise. Called per-upload rather than cached, since an admin
// changing the integration should take effect on the very next upload
// without an API restart.
func (s *StorageConfigService) BuildStore(ctx context.Context, tenantID uuid.UUID) (blobstore.Store, error) {
	cfg, err := s.Get(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("load storage config: %w", err)
	}
	if cfg == nil {
		return blobstore.NewLocalStore(s.uploadDir), nil
	}

	switch cfg.Provider {
	case domain.StorageProviderS3:
		secretKey, err := s.secrets.Resolve(ctx, cfg.S3SecretAccessKeySecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve s3 secret: %w", err)
		}
		return blobstore.NewS3Store(*cfg.S3Region, *cfg.S3AccessKeyID, secretKey, *cfg.S3Bucket), nil
	case domain.StorageProviderGCS:
		credentialsJSON, err := s.secrets.Resolve(ctx, cfg.GCSCredentialsJSONSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve gcs credentials: %w", err)
		}
		store, err := blobstore.NewGCSStore(ctx, credentialsJSON, *cfg.GCSBucket)
		if err != nil {
			return nil, fmt.Errorf("build gcs client: %w", err)
		}
		return store, nil
	default:
		return blobstore.NewLocalStore(s.uploadDir), nil
	}
}
