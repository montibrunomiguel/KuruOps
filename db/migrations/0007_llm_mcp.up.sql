-- Per-tenant LLM configuration. `kind = 'openai_compatible'` with a custom
-- base_url covers self-hosted runtimes (vLLM, Ollama, LM Studio) and most
-- enterprise-hosted providers without a dedicated adapter per vendor.
-- The API key itself is never stored here -- api_key_secret_ref points to an
-- entry in the secret manager (Vault / cloud KMS), resolved only inside the
-- worker process at call time.
create table llm_providers (
  id                 uuid primary key default gen_random_uuid(),
  tenant_id          uuid not null references tenants(id) on delete cascade,
  name               text not null,
  kind               llm_kind_enum not null,
  base_url           text,
  model              text not null,
  api_key_secret_ref text not null,
  is_default         boolean not null default false,
  created_by         uuid references users(id),
  created_at         timestamptz not null default now(),
  updated_at         timestamptz not null default now()
);

create unique index llm_providers_tenant_name_uq on llm_providers (tenant_id, name);
-- at most one default provider per tenant
create unique index llm_providers_one_default_per_tenant
  on llm_providers (tenant_id) where is_default;

-- Registered MCP servers a tenant can expose to the AI analysis agent.
-- allowed_tools is an explicit allow-list: tools the server exposes via
-- tools/list that are NOT in this array are never offered to the model.
create table mcp_servers (
  id                    uuid primary key default gen_random_uuid(),
  tenant_id             uuid not null references tenants(id) on delete cascade,
  name                  text not null,
  transport             mcp_transport_enum not null,
  endpoint_or_command   text not null,
  auth_secret_ref       text,
  allowed_tools         text[] not null default '{}',
  -- which analysis flows may use this server: 'alert_analysis', 'incident_analysis'
  enabled_for           text[] not null default '{}',
  -- tools outside this list always require analyst approval before execution,
  -- regardless of allowed_tools (e.g. isolate-host, block-ip)
  side_effecting_tools  text[] not null default '{}',
  is_enabled            boolean not null default true,
  created_by            uuid references users(id),
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now()
);

create unique index mcp_servers_tenant_name_uq on mcp_servers (tenant_id, name);

-- Forensic log of every tool invocation the AI agent made or proposed.
-- Side-effecting tools land here with status='proposed' and stay there
-- until an analyst approves or rejects them -- the agent never executes
-- a side-effecting tool on its own.
create table ai_tool_calls (
  id            bigint generated always as identity primary key,
  tenant_id     uuid not null references tenants(id) on delete cascade,
  mcp_server_id uuid not null references mcp_servers(id),
  tool_name     text not null,
  context_type  text not null,
  context_id    uuid not null,
  args          jsonb not null default '{}',
  result        jsonb,
  status        tool_call_status_enum not null default 'proposed',
  approved_by   uuid references users(id),
  approved_at   timestamptz,
  created_at    timestamptz not null default now(),
  constraint ai_tool_calls_context_type_check check (context_type in ('alert', 'incident'))
);

create index ai_tool_calls_tenant_idx on ai_tool_calls (tenant_id, created_at desc);
create index ai_tool_calls_context_idx on ai_tool_calls (context_type, context_id);
create index ai_tool_calls_pending_idx on ai_tool_calls (tenant_id) where status = 'proposed';
