-- Replaces the flat "one recurring weekday+time block per analyst" model
-- (on_call_shifts) with a rotation model: an ordered list of participants
-- who automatically hand over on a cadence (daily/weekly/custom N days),
-- optionally N at a time (concurrent_shifts), optionally restricted to
-- specific weekdays/hours, with per-day manual overrides. This is a clean
-- cut, not a data migration -- the two shapes aren't convertible, and
-- on_call_shifts holds only a handful of admin-configured rows, not
-- historical/audit data.
--
-- One schedule per tenant (tenant_id is unique) -- OnCallScheduleService.Get
-- auto-creates the row on first access so the frontend always has something
-- to render/edit, mirroring today's "one implicit schedule per tenant".
-- Timezone continues to live on tenants.timezone (0021_tenant_timezone), not
-- duplicated here.
drop table on_call_shifts;

create table on_call_schedules (
  id                 uuid primary key default gen_random_uuid(),
  tenant_id          uuid not null unique references tenants(id) on delete cascade,
  name               text not null default 'Primary On-Call',
  -- handover_at is a reference point in time (not just a time-of-day): the
  -- rotation's period count is computed as
  -- floor((now - handover_at) / (period_days * 24h)), so this anchors which
  -- participant group is "period 0".
  handover_at        timestamptz not null default now(),
  period_days        integer not null default 7 check (period_days > 0),
  concurrent_shifts  integer not null default 1 check (concurrent_shifts > 0),
  working_hours_mode text not null default 'all_day' check (working_hours_mode in ('all_day', 'specific_times')),
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now()
);

-- Ordered rotation membership -- position defines both display order and
-- the participant's slot in the rotation-group math (see
-- domain.ResolveOnCallSet).
create table on_call_rotation_participants (
  id           uuid primary key default gen_random_uuid(),
  schedule_id  uuid not null references on_call_schedules(id) on delete cascade,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  user_id      uuid not null references users(id),
  position     integer not null
);
create unique index on_call_participants_order_uq on on_call_rotation_participants (schedule_id, position);

-- Only consulted when working_hours_mode = 'specific_times'. Mirrors
-- playbook_phase_steps' pattern of a child table replaced wholesale on
-- every save rather than diffed.
create table on_call_working_hours (
  id            uuid primary key default gen_random_uuid(),
  schedule_id   uuid not null references on_call_schedules(id) on delete cascade,
  tenant_id     uuid not null references tenants(id) on delete cascade,
  weekdays      smallint[] not null,
  start_minute  smallint not null check (start_minute between 0 and 1439),
  end_minute    smallint not null check (end_minute between 0 and 1439)
);

-- A manual assignment for one calendar date that wins outright over the
-- computed rotation for that whole day (see domain.ResolveOnCallSet). One
-- override per schedule per date -- creating a second one for the same date
-- replaces the first (see OnCallScheduleRepository.CreateOverride's upsert).
create table on_call_overrides (
  id             uuid primary key default gen_random_uuid(),
  schedule_id    uuid not null references on_call_schedules(id) on delete cascade,
  tenant_id      uuid not null references tenants(id) on delete cascade,
  user_id        uuid not null references users(id),
  override_date  date not null,
  created_by     uuid references users(id),
  created_at     timestamptz not null default now()
);
create unique index on_call_overrides_date_uq on on_call_overrides (schedule_id, override_date);

do $$
declare
  t text;
begin
  foreach t in array array[
    'on_call_schedules', 'on_call_rotation_participants',
    'on_call_working_hours', 'on_call_overrides'
  ]
  loop
    execute format('alter table %I enable row level security', t);
    execute format('alter table %I force row level security', t);
    execute format(
      'create policy tenant_isolation on %I using (tenant_id = current_tenant_id()) with check (tenant_id = current_tenant_id())',
      t
    );
  end loop;
end $$;
