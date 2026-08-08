-- Replaces the three loose per-user access fields (role, resource_access,
-- allowed_tags) with a single reusable Role entity that bundles all three
-- -- product need: build a named role once ("SOC L1", "Analyst - Acme")
-- and assign it to any number of users, instead of re-picking the same
-- three settings on every user individually. is_admin replaces the old
-- 'admin' enum value as what gates Settings access; a plain boolean rather
-- than keeping a role-tier enum because, per a full audit of this codebase,
-- nothing ever branches on 'analyst' vs 'viewer' specifically -- every
-- other access decision already goes through resource_access/allowed_tags,
-- so keeping a separate tier label would just be a second, redundant
-- source of truth.
create table roles (
  id               uuid primary key default gen_random_uuid(),
  tenant_id        uuid not null references tenants(id) on delete cascade,
  name             text not null,
  is_admin         boolean not null default false,
  resource_access  text[] not null default '{}',
  allowed_tags     text[] not null default '{}',
  created_at       timestamptz not null default now(),
  updated_at       timestamptz not null default now(),
  constraint roles_resource_access_valid check (resource_access <@ array['alerts','incidents','followup']),
  unique (tenant_id, name)
);

alter table roles enable row level security;
alter table roles force row level security;

create policy tenant_isolation on roles
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());

-- Backfill: derive one role per distinct (tenant, is_admin, resource_access,
-- allowed_tags) combination actually in use today, across both users and
-- auth_group_mappings (a group mapping might not match any existing user's
-- exact combination yet). Named after whichever of the old role values
-- ('admin'/'analyst'/'viewer') was most common among the rows that
-- collapsed into it, disambiguated with a numeric suffix when a tenant ends
-- up with more than one role sharing that same base name (e.g. two
-- analysts scoped to different companies via allowed_tags produce
-- "Analyst" and "Analyst 2", not a collision). Every existing user's and
-- group mapping's effective access survives this exactly -- nothing left
-- for an admin to manually reconcile afterward; role names can always be
-- edited later from Settings -> Roles.
with source_rows as (
  select tenant_id, role, resource_access, allowed_tags, created_at from users
  union all
  select tenant_id, role, resource_access, allowed_tags, created_at from auth_group_mappings
),
combos as (
  select
    tenant_id,
    (role = 'admin') as is_admin,
    resource_access,
    allowed_tags,
    min(created_at) as first_seen,
    mode() within group (order by role) as typical_role
  from source_rows
  group by tenant_id, (role = 'admin'), resource_access, allowed_tags
),
named as (
  select
    tenant_id, is_admin, resource_access, allowed_tags, first_seen,
    initcap(typical_role::text) as base_name
  from combos
),
numbered as (
  select *,
    row_number() over (partition by tenant_id, base_name order by first_seen) as rn,
    count(*) over (partition by tenant_id, base_name) as name_count
  from named
)
insert into roles (tenant_id, name, is_admin, resource_access, allowed_tags)
select
  tenant_id,
  case when name_count = 1 then base_name else base_name || ' ' || rn::text end,
  is_admin,
  resource_access,
  allowed_tags
from numbered;

alter table users add column role_id uuid references roles(id);
update users u set role_id = r.id
from roles r
where r.tenant_id = u.tenant_id
  and r.is_admin = (u.role = 'admin')
  and r.resource_access = u.resource_access
  and r.allowed_tags = u.allowed_tags;
alter table users alter column role_id set not null;

alter table auth_group_mappings add column role_id uuid references roles(id);
update auth_group_mappings m set role_id = r.id
from roles r
where r.tenant_id = m.tenant_id
  and r.is_admin = (m.role = 'admin')
  and r.resource_access = m.resource_access
  and r.allowed_tags = m.allowed_tags;
alter table auth_group_mappings alter column role_id set not null;

alter table users drop constraint users_resource_access_valid;
alter table users drop column role;
alter table users drop column resource_access;
alter table users drop column allowed_tags;

alter table auth_group_mappings drop constraint auth_group_mappings_resource_access_valid;
alter table auth_group_mappings drop column role;
alter table auth_group_mappings drop column resource_access;
alter table auth_group_mappings drop column allowed_tags;

drop type user_role_enum;
