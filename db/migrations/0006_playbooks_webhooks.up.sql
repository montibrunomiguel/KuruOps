create table playbooks (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  title        text not null,
  category     text not null,
  description  text not null default '',
  -- substring-matched (case-insensitive) against alert titles for auto-suggestion
  keywords     text[] not null default '{}',
  created_by   uuid references users(id),
  created_at   timestamptz not null default now(),
  updated_at   timestamptz not null default now()
);

create index playbooks_tenant_keywords_gin on playbooks using gin (keywords);

create table playbook_phase_steps (
  id           uuid primary key default gen_random_uuid(),
  playbook_id  uuid not null references playbooks(id) on delete cascade,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  phase        incident_phase_enum not null,
  step_order   integer not null,
  action_text  text not null
);

create unique index playbook_phase_steps_order_uq on playbook_phase_steps (playbook_id, phase, step_order);

create table webhook_endpoints (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  name         text not null,
  source       text not null,
  -- the bearer token itself is never stored; only its hash is checked on ingest
  token_hash   text not null,
  token_last4  text not null,
  status       text not null default 'active',
  rotated_at   timestamptz,
  created_by   uuid references users(id),
  created_at   timestamptz not null default now(),
  constraint webhook_endpoints_status_check check (status in ('active', 'disabled'))
);

create unique index webhook_endpoints_tenant_name_uq on webhook_endpoints (tenant_id, name);
create index webhook_endpoints_token_hash_idx on webhook_endpoints (token_hash);

alter table alerts
  add constraint alerts_webhook_endpoint_id_fkey
  foreign key (webhook_endpoint_id) references webhook_endpoints(id) on delete set null;
