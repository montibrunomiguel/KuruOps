create table alerts (
  id                 uuid primary key default gen_random_uuid(),
  tenant_id          uuid not null references tenants(id) on delete cascade,
  -- id assigned by the source system (Wazuh alert_id, CrowdStrike detection id...);
  -- used for de-duplication on re-delivery of the same webhook.
  external_id        text,
  webhook_endpoint_id uuid,
  title              text not null,
  source             text not null,
  severity           severity_enum not null,
  original_severity  severity_enum not null,
  status             alert_status_enum not null default 'open',
  classification     classification_enum,
  close_comment      text,
  close_image_url    text,
  rule_id            text,
  asset              text,
  src_ip             inet,
  tags               text[] not null default '{}',
  payload            jsonb not null,
  incident_id        uuid,
  received_at        timestamptz not null default now(),
  acknowledged_at    timestamptz,
  closed_at          timestamptz,
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now(),
  constraint alerts_classification_requires_closed
    check (classification is null or status = 'closed'),
  constraint alerts_closed_requires_classification
    check (status <> 'closed' or classification is not null)
);

create unique index alerts_tenant_external_id_uq
  on alerts (tenant_id, source, external_id) where external_id is not null;
create index alerts_tenant_status_idx on alerts (tenant_id, status);
create index alerts_tenant_received_idx on alerts (tenant_id, received_at desc);
create index alerts_tenant_tags_gin on alerts using gin (tags);
create index alerts_incident_id_idx on alerts (incident_id);
create index alerts_payload_gin on alerts using gin (payload jsonb_path_ops);

create table alert_links (
  alert_id         uuid not null references alerts(id) on delete cascade,
  linked_alert_id  uuid not null references alerts(id) on delete cascade,
  tenant_id        uuid not null references tenants(id) on delete cascade,
  created_by       uuid references users(id),
  created_at       timestamptz not null default now(),
  primary key (alert_id, linked_alert_id),
  constraint alert_links_no_self_link check (alert_id <> linked_alert_id)
);

-- Append-only audit trail: every status change, severity override,
-- classification, tag edit, escalation, and AI analysis run is an event here.
-- Never update or delete rows -- corrections are new events, not mutations.
create table alert_events (
  id          bigint generated always as identity primary key,
  alert_id    uuid not null references alerts(id) on delete cascade,
  tenant_id   uuid not null references tenants(id) on delete cascade,
  event_type  text not null,
  actor_type  actor_type_enum not null,
  actor_id    uuid references users(id),
  data        jsonb not null default '{}',
  created_at  timestamptz not null default now()
);

create index alert_events_alert_id_idx on alert_events (alert_id, created_at);
create index alert_events_tenant_idx on alert_events (tenant_id, created_at desc);
