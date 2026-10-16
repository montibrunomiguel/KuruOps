-- attachment_blobs: where evidence attachments live when UPLOAD_BACKEND=database.
--
-- Until now an upload went to a local directory (UPLOAD_DIR) unless a tenant had
-- configured S3/GCS/Google Drive. A local directory is per-pod state: on Kubernetes
-- it is a ReadWriteOnce volume, which pins the api to ONE replica (a second replica
-- either cannot mount it or silently gets its own empty directory, so some analysts
-- would not see attachments others uploaded). Keeping the bytes in Postgres makes the
-- api stateless -- every replica sees every attachment -- with no extra infrastructure,
-- which is what a deployment that does not already run an object store needs to scale
-- the api.
--
-- It is a deliberate trade, not a replacement for object storage: attachments are
-- capped at 25 MB each (the upload handler's limit) and live in the database's backups
-- and WAL. A deployment with many or large attachments should configure S3/GCS under
-- Settings -> Storage Integration instead, which takes precedence per tenant.
--
-- (tenant_id, key): the key is the opaque path the upload handler generates
-- (Alert/2026/10/15/<title>/<uuid>_<file>.png); it is unique per tenant, not globally,
-- so two tenants can never collide or read each other's.
create table attachment_blobs (
  tenant_id    uuid        not null references tenants(id) on delete cascade,
  key          text        not null,
  content_type text        not null,
  size_bytes   bigint      not null check (size_bytes >= 0),
  data         bytea       not null,
  created_at   timestamptz not null default now(),
  primary key (tenant_id, key)
);

alter table attachment_blobs enable row level security;
alter table attachment_blobs force row level security;

create policy tenant_isolation on attachment_blobs
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
