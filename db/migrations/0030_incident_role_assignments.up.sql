-- Per-incident NIST-800-61-style team roles, additive to the existing
-- generic incident_assignees ("who's working this") -- see that table's
-- own comment. Commander and Technical Lead are single-assignee (a partial
-- unique index per role, enforced here rather than only in application
-- code so a race between two concurrent requests can't both succeed); the
-- other three roles (Incident Handler, Communications Lead, Privacy
-- Officer) allow any number of people. The same person can hold more than
-- one role on the same incident (e.g. also being the sole Technical Lead
-- while also listed as an Incident Handler) -- nothing here prevents that,
-- only same-person-same-role duplicates (redundant anyway) via the primary key.
create table incident_role_assignments (
  incident_id  uuid not null references incidents(id) on delete cascade,
  user_id      uuid not null references users(id),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  role         text not null check (role in (
    'commander', 'incident_handler', 'communications_lead', 'privacy_officer', 'technical_lead'
  )),
  created_at   timestamptz not null default now(),
  primary key (incident_id, user_id, role)
);

create index incident_role_assignments_incident_idx on incident_role_assignments (incident_id);

-- Single-assignee roles: a partial unique index per role means at most one
-- row can exist for a given incident with that role, regardless of which
-- user_id it names -- this is what actually stops a second Commander (or
-- Technical Lead) from being assigned, not just application-layer checks.
create unique index incident_role_assignments_single_commander
  on incident_role_assignments (incident_id)
  where role = 'commander';

create unique index incident_role_assignments_single_technical_lead
  on incident_role_assignments (incident_id)
  where role = 'technical_lead';

alter table incident_role_assignments enable row level security;
alter table incident_role_assignments force row level security;

create policy tenant_isolation on incident_role_assignments
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
