-- Alert comments mirror incident_comments exactly (see 0005_incidents.up.sql)
-- -- a "Team Notes" thread for alerts, the same feature incidents have had
-- since day one. author_name is included from the start here (incidents
-- needed a follow-up migration, 0017, to add it after the fact) since the
-- author-name-resolution pattern in IncidentHandlers.addComment is already
-- established and AlertHandlers.addComment follows it from the outset.
create table alert_comments (
  id           uuid primary key default gen_random_uuid(),
  alert_id     uuid not null references alerts(id) on delete cascade,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  author_id    uuid not null references users(id),
  author_name  text not null,
  body         text not null,
  image_url    text,
  created_at   timestamptz not null default now()
);

create index alert_comments_alert_id_idx on alert_comments (alert_id, created_at);

alter table alert_comments enable row level security;
alter table alert_comments force row level security;

create policy tenant_isolation on alert_comments
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
