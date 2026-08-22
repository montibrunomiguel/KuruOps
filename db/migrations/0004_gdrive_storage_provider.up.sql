-- Adds Google Drive as a third tenant_storage_config provider, alongside
-- s3/gcs (see 0001_initial_schema.up.sql). Unlike S3/GCS, Drive is offered
-- with a choice of two auth methods -- a pasted service-account JSON key
-- (same UX as GCS today) or a full "Connect your Google account" OAuth
-- flow (see internal/blobstore.GDriveStore and
-- StorageConfigService.SaveGDriveServiceAccount/HandleGDriveOAuthCallback)
-- -- so gdrive_auth_method discriminates which of the two secret-ref
-- columns below is populated.
--
-- gdrive_oauth_connected_email is NOT a secret -- just the connected
-- account's address, kept in the row itself for display, same reasoning
-- token_last4 already gets on webhook_endpoints/user_api_tokens (a
-- non-sensitive fragment of an otherwise-opaque credential, safe to show
-- back to an admin).
alter table tenant_storage_config add column gdrive_folder_id text;
alter table tenant_storage_config add column gdrive_auth_method text;
alter table tenant_storage_config add column gdrive_service_account_json_secret_ref text;
alter table tenant_storage_config add column gdrive_oauth_refresh_token_secret_ref text;
alter table tenant_storage_config add column gdrive_oauth_connected_email text;

-- CHECK constraints can't be altered in place -- drop and recreate both
-- of tenant_storage_config's existing ones with the gdrive branch added.
alter table tenant_storage_config drop constraint tenant_storage_config_provider_check;
alter table tenant_storage_config add constraint tenant_storage_config_provider_check
  check (provider in ('s3', 'gcs', 'gdrive'));

alter table tenant_storage_config add constraint tenant_storage_config_gdrive_auth_method_check
  check (gdrive_auth_method is null or gdrive_auth_method in ('service_account', 'oauth'));

alter table tenant_storage_config drop constraint tenant_storage_config_provider_fields;
alter table tenant_storage_config add constraint tenant_storage_config_provider_fields check (
  (provider = 's3' and s3_bucket is not null and s3_region is not null
     and s3_access_key_id is not null and s3_secret_access_key_secret_ref is not null)
  or (provider = 'gcs' and gcs_bucket is not null and gcs_project_id is not null
     and gcs_credentials_json_secret_ref is not null)
  or (provider = 'gdrive' and gdrive_folder_id is not null and (
       (gdrive_auth_method = 'service_account' and gdrive_service_account_json_secret_ref is not null)
       or
       (gdrive_auth_method = 'oauth' and gdrive_oauth_refresh_token_secret_ref is not null)
     ))
);
