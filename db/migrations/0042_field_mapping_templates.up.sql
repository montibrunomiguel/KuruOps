-- Field mapping templates: an admin-managed catalog of JSON-path -> display
-- label rules (see Settings -> Field Mapping Templates) that lets an
-- endpoint's ingest pull extra fields into an alert's metadata panel beyond
-- whatever the sender put in the webhook body's own top-level "metadata"
-- object (see 0032_alert_metadata.up.sql / internal/ingest/handler.go's
-- extractMetadata). A template is reusable across several webhook_endpoints
-- (see 0043_webhook_endpoints_field_mapping_template.up.sql), not 1:1 with
-- one -- e.g. one "CrowdStrike fields" template assigned to every
-- CrowdStrike-sourced endpoint.
--
-- rules is a jsonb array of {"jsonPath": "...", "label": "..."} objects --
-- same "structured list in one jsonb column" shape as
-- ai_analysis_runs.messages (0028_ai_analysis_runs.up.sql), not one column
-- per rule, since the rule count is open-ended and the whole array is
-- always read/written together, never queried by individual rule.
create table field_mapping_templates (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  name        text not null,
  rules       jsonb not null default '[]'::jsonb,
  created_by  uuid references users(id),
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create unique index field_mapping_templates_tenant_name_uq on field_mapping_templates (tenant_id, lower(name));

alter table field_mapping_templates enable row level security;
alter table field_mapping_templates force row level security;

-- Only the standard tenant-scoped policy -- unlike webhook_endpoints/
-- user_api_tokens, this table is never looked up before a tenant is known;
-- cmd/ingest only resolves a template after the webhook token has already
-- resolved endpoint.TenantID, so no pre-tenant-context lookup carve-out is
-- needed here.
create policy tenant_isolation on field_mapping_templates
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
