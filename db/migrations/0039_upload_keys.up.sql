-- Maps an uploaded attachment's storage key back to the alert/incident it
-- belongs to, so GET /api/v1/uploads/images/* (uploads.go's serve) can
-- reapply the same allowedTags visibility rule POST already enforces via
-- resolveEntity. The key itself can't be reverse-parsed for this: its
-- <Alert|Incident>/yyyy/mm/dd/<slugified title>/<uuid>_<name>.ext layout
-- never encodes the real alert/incident UUID, and the slugified title is
-- lossy (truncated at 80 chars, ambiguous between similar titles) -- a
-- proper lookup table is the only reliable way back to the owning entity.
create table upload_keys (
  key          text primary key,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  context_type text not null,
  context_id   uuid not null,
  created_at   timestamptz not null default now(),
  constraint upload_keys_context_type_check check (context_type in ('alert', 'incident'))
);

-- Every checkTagAccess lookup filters by (tenant_id, key) -- key alone is
-- already unique (primary key), but including tenant_id keeps the query
-- plan a single index-only lookup under RLS instead of an extra filter
-- step.
create index upload_keys_tenant_id_key_idx on upload_keys (tenant_id, key);

alter table upload_keys enable row level security;
alter table upload_keys force row level security;

create policy tenant_isolation on upload_keys
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
