package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

// StorageConfigRepository is the single-row-per-tenant "which cloud bucket
// (if any) holds alert/incident evidence" config -- same shape as
// IdentityConfigRepository's LDAP/SAML methods (tenant_id is the row's PK,
// so Upsert always replaces the whole row rather than merging fields).
type StorageConfigRepository struct{}

func NewStorageConfigRepository() *StorageConfigRepository {
	return &StorageConfigRepository{}
}

func (r *StorageConfigRepository) Get(ctx context.Context, tx pgx.Tx) (*domain.StorageConfig, error) {
	var c domain.StorageConfig
	err := tx.QueryRow(ctx, `
		select tenant_id, provider,
		       s3_bucket, s3_region, s3_access_key_id, s3_secret_access_key_secret_ref,
		       gcs_bucket, gcs_project_id, gcs_credentials_json_secret_ref,
		       gdrive_folder_id, gdrive_auth_method, gdrive_service_account_json_secret_ref,
		       gdrive_oauth_refresh_token_secret_ref, gdrive_oauth_connected_email,
		       created_at, updated_at
		from tenant_storage_config limit 1`,
	).Scan(
		&c.TenantID, &c.Provider,
		&c.S3Bucket, &c.S3Region, &c.S3AccessKeyID, &c.S3SecretAccessKeySecretRef,
		&c.GCSBucket, &c.GCSProjectID, &c.GCSCredentialsJSONSecretRef,
		&c.GDriveFolderID, &c.GDriveAuthMethod, &c.GDriveServiceAccountJSONSecretRef,
		&c.GDriveOAuthRefreshTokenSecretRef, &c.GDriveOAuthConnectedEmail,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get storage config: %w", err)
	}
	return &c, nil
}

// Upsert replaces the tenant's storage config wholesale -- switching
// provider (s3/gcs/gdrive, or between gdrive's two auth methods) leaves
// every other provider/method's columns null, matching how the config is
// always saved as one complete unit (see StorageConfigService.SaveS3/
// SaveGCS/SaveGDriveServiceAccount/HandleGDriveOAuthCallback), never a
// partial field update.
func (r *StorageConfigRepository) Upsert(ctx context.Context, tx pgx.Tx, c *domain.StorageConfig) error {
	_, err := tx.Exec(ctx, `
		insert into tenant_storage_config (
			tenant_id, provider,
			s3_bucket, s3_region, s3_access_key_id, s3_secret_access_key_secret_ref,
			gcs_bucket, gcs_project_id, gcs_credentials_json_secret_ref,
			gdrive_folder_id, gdrive_auth_method, gdrive_service_account_json_secret_ref,
			gdrive_oauth_refresh_token_secret_ref, gdrive_oauth_connected_email
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		on conflict (tenant_id) do update set
			provider = excluded.provider,
			s3_bucket = excluded.s3_bucket, s3_region = excluded.s3_region,
			s3_access_key_id = excluded.s3_access_key_id,
			s3_secret_access_key_secret_ref = excluded.s3_secret_access_key_secret_ref,
			gcs_bucket = excluded.gcs_bucket, gcs_project_id = excluded.gcs_project_id,
			gcs_credentials_json_secret_ref = excluded.gcs_credentials_json_secret_ref,
			gdrive_folder_id = excluded.gdrive_folder_id,
			gdrive_auth_method = excluded.gdrive_auth_method,
			gdrive_service_account_json_secret_ref = excluded.gdrive_service_account_json_secret_ref,
			gdrive_oauth_refresh_token_secret_ref = excluded.gdrive_oauth_refresh_token_secret_ref,
			gdrive_oauth_connected_email = excluded.gdrive_oauth_connected_email,
			updated_at = now()`,
		c.TenantID, c.Provider,
		c.S3Bucket, c.S3Region, c.S3AccessKeyID, c.S3SecretAccessKeySecretRef,
		c.GCSBucket, c.GCSProjectID, c.GCSCredentialsJSONSecretRef,
		c.GDriveFolderID, c.GDriveAuthMethod, c.GDriveServiceAccountJSONSecretRef,
		c.GDriveOAuthRefreshTokenSecretRef, c.GDriveOAuthConnectedEmail,
	)
	if err != nil {
		return fmt.Errorf("upsert storage config: %w", err)
	}
	return nil
}

// Delete turns off the cloud integration entirely -- uploads revert to
// local disk (see handlers.UploadHandlers) the moment this row is gone.
func (r *StorageConfigRepository) Delete(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `delete from tenant_storage_config`)
	if err != nil {
		return fmt.Errorf("delete storage config: %w", err)
	}
	return nil
}
