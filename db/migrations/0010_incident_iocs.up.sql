-- incident_iocs: Indicators of Compromise recorded against an incident --
-- see backend/internal/domain/ioc.go's doc comment for the NIST SP 800-61r3
-- / SP 800-150 / STIX 2.1 sourcing of the `type` values. Deliberately not a
-- domain enum type (unlike e.g. alert_status_enum) -- the list is expected
-- to grow (a new IOC type showing up in the wild shouldn't need a schema
-- migration), so validation lives at the service layer
-- (domain.IOCTypeIsValid) instead, same choice already made for
-- webhook_endpoints.source.
--
-- Scoped to exactly one incident, append-only (no update/delete route) --
-- same shape as incident_comments, whose author_id/created_at already
-- double as this table's audit trail (who added an indicator, when)
-- without a separate incident_events row.
create table incident_iocs (
  id uuid primary key default gen_random_uuid(),
  incident_id uuid not null references incidents(id) on delete cascade,
  tenant_id uuid not null references tenants(id) on delete cascade,
  type text not null,
  value text not null,
  description text not null default '',
  identified_at timestamptz not null,
  created_by uuid not null references users(id),
  -- created_by_name is denormalized at write time, same reasoning as
  -- incident_comments.author_name (see that column's own history for why:
  -- a non-admin analyst has no user-lookup endpoint to resolve an id back
  -- to a display name).
  created_by_name text not null,
  created_at timestamptz not null default now()
);

create index incident_iocs_incident_id_idx on incident_iocs (incident_id, identified_at);

alter table incident_iocs enable row level security;
alter table incident_iocs force row level security;

create policy tenant_isolation on incident_iocs
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
