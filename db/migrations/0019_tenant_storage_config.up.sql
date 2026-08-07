-- One evidence-storage integration per tenant (S3 or GCS), same
-- single-row-per-tenant shape as tenant_ldap_config/tenant_saml_config in
-- 0011_identity_provider_config.up.sql. When no row exists, uploads fall
-- back to local disk (see handlers.UploadHandlers) -- this table only
-- exists to opt INTO cloud storage, not to require it.
--
-- Only one provider is active at a time: saving an S3 config overwrites any
-- existing GCS config in the same row (and vice versa) via upsert, enforced
-- by application logic (StorageConfigService.Save*), not a DB constraint --
-- the provider-specific columns for the inactive provider are simply left
-- null.
create table tenant_storage_config (
  tenant_id                       uuid primary key references tenants(id) on delete cascade,
  provider                        text not null check (provider in ('s3', 'gcs')),

  s3_bucket                       text,
  s3_region                       text,
  s3_access_key_id                text,
  s3_secret_access_key_secret_ref text,

  gcs_bucket                      text,
  gcs_project_id                  text,
  gcs_credentials_json_secret_ref text,

  created_at                      timestamptz not null default now(),
  updated_at                      timestamptz not null default now(),

  constraint tenant_storage_config_provider_fields check (
    (provider = 's3' and s3_bucket is not null and s3_region is not null
       and s3_access_key_id is not null and s3_secret_access_key_secret_ref is not null)
    or
    (provider = 'gcs' and gcs_bucket is not null and gcs_project_id is not null
       and gcs_credentials_json_secret_ref is not null)
  )
);

alter table tenant_storage_config enable row level security;
alter table tenant_storage_config force row level security;

create policy tenant_isolation on tenant_storage_config
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
