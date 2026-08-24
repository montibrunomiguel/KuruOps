-- admin_audit_events: an append-only record of who changed what in
-- Settings, and when -- separate from alert_events/incident_events (which
-- cover alert/incident state changes, not admin configuration). `area`
-- names which Settings panel the change came from (e.g. "webhooks",
-- "users", "retention"), matching the values SettingsLayout.tsx's nav
-- already uses so a future filter UI can reuse the same vocabulary.
-- `action` is a short verb ("create"/"update"/"delete"/"connect"/
-- "disconnect"/...), and `data` carries a before/after diff in the same
-- {"from":...,"to":...} shape alert_events/incident_events already use --
-- deliberately schemaless (jsonb) since every area's diff shape differs.
--
-- actor_id is NOT NULL (unlike alert_events.actor_id, which is nullable to
-- allow system/AI actors): every mutation this table records comes from an
-- authenticated admin's own action on their own Settings page -- there is
-- no system- or AI-triggered write into any of the 15 services this feeds
-- from. reuses actor_type_enum from 0001_initial_schema for consistency
-- with alert_events/incident_events even though 'user' is the only value
-- expected in practice today.
create table admin_audit_events (
  id bigint generated always as identity primary key,
  tenant_id uuid not null references tenants(id) on delete cascade,
  area text not null,
  action text not null,
  actor_type public.actor_type_enum not null default 'user',
  actor_id uuid not null,
  data jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

-- (tenant_id, created_at desc, id desc) backs both RLS-scoped access and
-- the newest-first keyset pagination AdminAuditEventRepository.List uses --
-- id as the keyset tiebreaker since created_at alone isn't unique enough
-- across a burst of same-millisecond writes.
create index admin_audit_events_tenant_created_idx on admin_audit_events (tenant_id, created_at desc, id desc);

alter table admin_audit_events enable row level security;
alter table admin_audit_events force row level security;

create policy tenant_isolation on admin_audit_events
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
