-- Personal API tokens: a user's own bearer-token alternative to a JWT
-- session, generated and revoked from their own Profile page (self-service,
-- see AccountHandlers) rather than admin-managed like webhook_endpoints.
-- Same secret-handling shape as webhook_endpoints (0006_playbooks_webhooks):
-- the plaintext token is never stored, only its SHA-256 hash + last 4
-- characters for display. A token carries no permissions of its own --
-- middleware.JWTAuth resolves it back to its owning user and re-derives
-- Claims from that user's CURRENT Role on every request, so revoking a
-- user's access (deactivating them, changing their role) takes effect on
-- their personal tokens immediately, with nothing to keep in sync.
create table user_api_tokens (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  user_id      uuid not null references users(id) on delete cascade,
  name         text not null,
  token_hash   text not null,
  token_last4  text not null,
  expires_at   timestamptz,
  created_at   timestamptz not null default now(),
  revoked_at   timestamptz
);

create index user_api_tokens_user_id_idx on user_api_tokens (user_id);
create index user_api_tokens_token_hash_idx on user_api_tokens (token_hash);

alter table user_api_tokens enable row level security;
alter table user_api_tokens force row level security;

create policy tenant_isolation on user_api_tokens
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());

-- The auth middleware authenticates a personal API token BEFORE it knows
-- which tenant issued it -- same shape as the webhook token lookup carve-out
-- (0010_webhook_token_lookup_policy.up.sql): a second permissive SELECT
-- policy that only opens up when the caller explicitly sets
-- app.api_token_lookup, scoped to this table only. The hashed, random token
-- in the WHERE clause is still the actual authentication boundary; this
-- policy only lets the query run at all, not list rows freely.
create policy api_token_lookup on user_api_tokens
  for select
  using (current_setting('app.api_token_lookup', true) = 'true');
