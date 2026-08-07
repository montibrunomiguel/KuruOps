create table incidents (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  title        text not null,
  description  text not null default '',
  severity     severity_enum not null,
  priority     incident_priority_enum not null,
  phase        incident_phase_enum not null default 'new',
  owner_id     uuid references users(id),
  tags         text[] not null default '{}',
  sla_due_at   timestamptz,
  sla_breached boolean not null default false,
  opened_at    timestamptz not null default now(),
  closed_at    timestamptz,
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create index incidents_tenant_phase_idx on incidents (tenant_id, phase);
create index incidents_tenant_opened_idx on incidents (tenant_id, opened_at desc);
create index incidents_tenant_tags_gin on incidents using gin (tags);

alter table alerts
  add constraint alerts_incident_id_fkey
  foreign key (incident_id) references incidents(id) on delete set null;

create table incident_alert_links (
  incident_id  uuid not null references incidents(id) on delete cascade,
  alert_id     uuid not null references alerts(id) on delete cascade,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  linked_at    timestamptz not null default now(),
  primary key (incident_id, alert_id)
);

-- One row per NIST phase the incident has entered. Never UPDATE entered_at
-- directly -- audit corrections write corrected_entered_at plus who/when/why
-- in corrected_at/corrected_by/correction_reason, so the originally recorded
-- entered_at is preserved even after a correction. Readers that want "the
-- current best timestamp" use coalesce(corrected_entered_at, entered_at).
create table incident_status_history (
  id                     uuid primary key default gen_random_uuid(),
  incident_id            uuid not null references incidents(id) on delete cascade,
  tenant_id              uuid not null references tenants(id) on delete cascade,
  phase                  incident_phase_enum not null,
  entered_at             timestamptz not null default now(),
  corrected_entered_at   timestamptz,
  corrected_at           timestamptz,
  corrected_by           uuid references users(id),
  correction_reason      text,
  created_at             timestamptz not null default now(),
  constraint incident_status_history_correction_requires_reason
    check (corrected_at is null or (correction_reason is not null and corrected_entered_at is not null))
);

create unique index incident_status_history_phase_uq on incident_status_history (incident_id, phase);

-- System + user generated audit trail (status changes, alert links,
-- isolation actions, AI analysis runs). Append-only, same rule as alert_events.
create table incident_events (
  id          bigint generated always as identity primary key,
  incident_id uuid not null references incidents(id) on delete cascade,
  tenant_id   uuid not null references tenants(id) on delete cascade,
  event_type  text not null,
  actor_type  actor_type_enum not null,
  actor_id    uuid references users(id),
  data        jsonb not null default '{}',
  created_at  timestamptz not null default now()
);

create index incident_events_incident_id_idx on incident_events (incident_id, created_at);
create index incident_events_tenant_idx on incident_events (tenant_id, created_at desc);

create table incident_comments (
  id           uuid primary key default gen_random_uuid(),
  incident_id  uuid not null references incidents(id) on delete cascade,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  author_id    uuid not null references users(id),
  body         text not null,
  image_url    text,
  created_at   timestamptz not null default now()
);

create index incident_comments_incident_id_idx on incident_comments (incident_id, created_at);
