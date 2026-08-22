-- Shared CSRF-protection table for every 3-legged OAuth flow this app
-- initiates (Google Drive's "Connect your Google account", and Slack's
-- workspace-connect flow landing in a later migration) -- a single-use,
-- short-lived, DB-backed `state` token, generated when an admin clicks
-- "Connect" and consumed exactly once on the provider's callback. Same
-- shape as password_reset_tokens/refresh_tokens (a hashed, expiring,
-- single-use credential artifact tied to the user who requested it), not
-- a per-provider signed/stateless token -- reuses that already-proven
-- pattern instead of inventing a second one, and avoids the cookie/
-- SameSite class of bug a cross-site redirect can hit (see
-- tenant_saml_config's ACS flow, which works around exactly that with
-- RelayState).
--
-- metadata carries whatever small bit of context needs to survive the
-- redirect round trip that doesn't belong in the URL (e.g. Google Drive's
-- chosen target folder ID) -- provider-specific, deliberately untyped.
create table oauth_states (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  user_id      uuid not null references users(id) on delete cascade,
  provider     text not null check (provider in ('gdrive', 'slack')),
  token_hash   text not null,
  metadata     jsonb not null default '{}',
  expires_at   timestamptz not null,
  consumed_at  timestamptz,
  created_at   timestamptz not null default now(),
  constraint oauth_states_token_hash_key unique (token_hash)
);

alter table oauth_states enable row level security;
alter table oauth_states force row level security;

create policy tenant_isolation on oauth_states
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
