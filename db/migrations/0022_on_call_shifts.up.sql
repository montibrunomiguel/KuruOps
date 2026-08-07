-- on_call_shifts backs Settings -> On-Call Schedule: a weekly recurring
-- shift (weekday + time-of-day range) assigning one analyst to cover new
-- alerts as they're ingested. start_minute/end_minute are minutes-since-
-- midnight (0-1439) in the tenant's configured timezone (tenants.timezone,
-- see 0021_tenant_timezone.up.sql), not a Postgres `time` column -- storing
-- a plain int sidesteps pgx's lack of a default `time` codec and keeps
-- wraparound-shift arithmetic (e.g. 22:00-06:00) simple integer comparisons
-- in Go rather than interval math in SQL.
--
-- Overlapping shifts (multiple analysts covering the same slot) are
-- intentionally allowed, not rejected at write time -- a real SOC schedule
-- legitimately has backup coverage. OnCallShiftRepository.ResolveCurrentAnalyst
-- picks a uniformly random match (order by random()) when more than one
-- shift is active, so alert volume splits roughly evenly across everyone
-- covering the same slot instead of always landing on one analyst.
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
