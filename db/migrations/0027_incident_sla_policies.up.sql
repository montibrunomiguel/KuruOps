-- incident_sla_policies lets an admin configure, per (severity, priority)
-- pair, how many minutes an incident has before it's considered SLA
-- breached. No row for a pair = unconfigured, not an error -- SLA policy is
-- opt-in per pair (see IncidentSLAService.Lookup). incidents.sla_due_at is
-- computed from this at create time and whenever severity/priority change
-- (IncidentService), and cmd/worker sweeps sla_due_at against now() to flip
-- incidents.sla_breached.
create table incident_sla_policies (
  id                uuid primary key default gen_random_uuid(),
  tenant_id         uuid not null references tenants(id) on delete cascade,
  severity          severity_enum not null,
  priority          incident_priority_enum not null,
  due_within_minutes integer not null check (due_within_minutes > 0),
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  unique (tenant_id, severity, priority)
);

alter table incident_sla_policies enable row level security;
alter table incident_sla_policies force row level security;

create policy tenant_isolation on incident_sla_policies
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
