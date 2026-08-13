-- Lets a user call the API programmatically (scripts, integrations,
-- automation) from Settings -> My Account, without a 15-minute session
-- JWT. Hashed at rest (sha256) and shown once in the create response, same
-- convention as webhook_endpoints.token_hash and refresh_tokens.token_hash
-- (WebhookService/AuthService's generateToken/hashToken). Structurally
-- closest to refresh_tokens (per-user, not per-tenant-wide like a webhook
-- endpoint) -- see that table's own comment for the rotation/revocation
-- reasoning this one borrows.
--
-- Unlike a session JWT (whose claims are a snapshot taken at login time,
-- stale until the token's next refresh), a personal access token is looked
-- up by hash on every request (see middleware's PAT auth path) -- so it
-- always reflects the holder's *current* Role, not a 15-minute-old one.
create table personal_access_tokens (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  user_id      uuid not null references users(id) on delete cascade,
  name         text not null,
  token_hash   text not null unique,
  token_last4  text not null,
  expires_at   timestamptz,
  last_used_at timestamptz,
  revoked_at   timestamptz,
  created_at   timestamptz not null default now()
);

create index personal_access_tokens_user_idx on personal_access_tokens (user_id);

alter table personal_access_tokens enable row level security;
alter table personal_access_tokens force row level security;

create policy tenant_isolation on personal_access_tokens
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
