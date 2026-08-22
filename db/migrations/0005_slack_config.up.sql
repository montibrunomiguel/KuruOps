-- tenant_slack_config: a Slack workspace connected via bot-token OAuth
-- (see internal/service.SlackConfigService, internal/slackclient). Same
-- single-row-per-tenant shape as tenant_smtp_config/tenant_storage_config
-- -- tenant_id is the row's own PK, so connecting overwrites whatever was
-- there before rather than merging fields.
--
-- This is the foundation only: connect/disconnect a workspace and expose
-- "is Slack configured" for later features to gate on. No channel/message
-- sync tables yet -- those arrive with the phase that actually needs them.
create table tenant_slack_config (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  bot_token_secret_ref text not null,
  team_id text not null,
  team_name text not null,
  bot_user_id text not null,
  -- Meaningful authorship (who connected this workspace), same convention
  -- as webhook_endpoints.created_by -- default RESTRICT (no ON DELETE
  -- clause) rather than SET NULL, so deleting the installing user doesn't
  -- silently erase who set this up.
  installed_by_user_id uuid not null references users(id),
  -- Comma-separated, exactly what Slack's oauth.v2.access response
  -- returned -- display/debug only, never parsed back for authorization
  -- decisions.
  granted_scopes text not null default '',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

alter table tenant_slack_config enable row level security;
alter table tenant_slack_config force row level security;

create policy tenant_isolation on tenant_slack_config
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
