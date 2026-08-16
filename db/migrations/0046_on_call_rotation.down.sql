drop table if exists on_call_overrides;
drop table if exists on_call_working_hours;
drop table if exists on_call_rotation_participants;
drop table if exists on_call_schedules;

create table on_call_shifts (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  user_id       uuid not null references users(id),
  weekday       smallint not null check (weekday between 0 and 6),
  start_minute  smallint not null check (start_minute between 0 and 1439),
  end_minute    smallint not null check (end_minute between 0 and 1439),
  created_at    timestamptz not null default now()
);

create index on_call_shifts_tenant_weekday_idx on on_call_shifts (tenant_id, weekday);

alter table on_call_shifts enable row level security;
alter table on_call_shifts force row level security;

create policy tenant_isolation on on_call_shifts
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
