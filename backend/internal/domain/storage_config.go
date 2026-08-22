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
	StorageProviderS3     StorageProvider = "s3"
	StorageProviderGCS    StorageProvider = "gcs"
	StorageProviderGDrive StorageProvider = "gdrive"
)

// GDriveAuthMethod selects how a StorageConfig with Provider ==
// StorageProviderGDrive authenticates against the Drive API -- see
// blobstore.GDriveStore's two constructors.
type GDriveAuthMethod string

const (
	GDriveAuthMethodServiceAccount GDriveAuthMethod = "service_account"
	GDriveAuthMethodOAuth          GDriveAuthMethod = "oauth"
)

// StorageConfig mirrors `tenant_storage_config` (see
// db/migrations/0001_initial_schema.up.sql) -- same secret-reference
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

	GDriveFolderID                    *string           `json:"gdriveFolderId,omitempty"`
	GDriveAuthMethod                  *GDriveAuthMethod `json:"gdriveAuthMethod,omitempty"`
	GDriveServiceAccountJSONSecretRef string            `json:"-"`
	GDriveOAuthRefreshTokenSecretRef  string            `json:"-"`
	GDriveOAuthConnectedEmail         *string           `json:"gdriveOauthConnectedEmail,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
