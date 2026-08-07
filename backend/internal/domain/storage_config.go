package domain

import (
	"time"

	"github.com/google/uuid"
)

// StorageProvider selects which cloud object-storage integration is active
// for a tenant. Empty/absent (no domain.StorageConfig row) means "local
// disk" -- see handlers.UploadHandlers and blobstore.Store.
type StorageProvider string

const (
	StorageProviderS3  StorageProvider = "s3"
	StorageProviderGCS StorageProvider = "gcs"
)

// StorageConfig mirrors `tenant_storage_config` (see
// db/migrations/0019_tenant_storage_config.up.sql) -- same secret-reference
// pattern as domain.LDAPConfig/domain.SAMLConfig: the actual access
// key/service-account JSON is never stored here, only an opaque reference
// into secrets.Store, and those reference fields are tagged json:"-" so an
// admin's Settings GET can never leak them back out.
type StorageConfig struct {
	TenantID uuid.UUID       `json:"tenantId"`
	Provider StorageProvider `json:"provider"`

	S3Bucket                   *string `json:"s3Bucket,omitempty"`
	S3Region                   *string `json:"s3Region,omitempty"`
	S3AccessKeyID              *string `json:"s3AccessKeyId,omitempty"`
	S3SecretAccessKeySecretRef string  `json:"-"`

	GCSBucket                   *string `json:"gcsBucket,omitempty"`
	GCSProjectID                *string `json:"gcsProjectId,omitempty"`
	GCSCredentialsJSONSecretRef string  `json:"-"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
