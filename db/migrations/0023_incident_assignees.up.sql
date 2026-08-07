-- Incident ownership becomes multi-analyst: incidents.owner_id (single,
-- nullable) is replaced by incident_assignees, a join table supporting zero
-- or more analysts per incident. Existing owner_id data is migrated in,
-- then the column is dropped -- see IncidentRepository.SetAssignees for the
-- "delete-then-bulk-insert" write pattern used against this table.
create table incident_assignees (
  incident_id  uuid not null references incidents(id) on delete cascade,
  user_id      uuid not null references users(id),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  created_at   timestamptz not null default now(),
  primary key (incident_id, user_id)
);

create index incident_assignees_incident_idx on incident_assignees (incident_id);

alter table incident_assignees enable row level security;
alter table incident_assignees force row level security;

create policy tenant_isolation on incident_assignees
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());

insert into incident_assignees (incident_id, user_id, tenant_id)
select id, owner_id, tenant_id from incidents where owner_id is not null;

alter table incidents drop column owner_id;
