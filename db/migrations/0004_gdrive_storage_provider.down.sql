alter table tenant_storage_config drop constraint tenant_storage_config_provider_fields;
alter table tenant_storage_config drop constraint tenant_storage_config_gdrive_auth_method_check;
alter table tenant_storage_config drop constraint tenant_storage_config_provider_check;

alter table tenant_storage_config add constraint tenant_storage_config_provider_check
  check (provider in ('s3', 'gcs'));
alter table tenant_storage_config add constraint tenant_storage_config_provider_fields check (
  (provider = 's3' and s3_bucket is not null and s3_region is not null
     and s3_access_key_id is not null and s3_secret_access_key_secret_ref is not null)
  or (provider = 'gcs' and gcs_bucket is not null and gcs_project_id is not null
     and gcs_credentials_json_secret_ref is not null)
);

alter table tenant_storage_config drop column gdrive_oauth_connected_email;
alter table tenant_storage_config drop column gdrive_oauth_refresh_token_secret_ref;
alter table tenant_storage_config drop column gdrive_service_account_json_secret_ref;
alter table tenant_storage_config drop column gdrive_auth_method;
alter table tenant_storage_config drop column gdrive_folder_id;
