-- password_reset_tokens backs the self-service "forgot my password" flow
-- (PasswordResetService). Hashed at rest (sha256) and single-use (used_at),
-- same convention as refresh_tokens.token_hash /
-- webhook_endpoints.token_hash -- the plaintext token only ever lives in
-- the reset-link email and the one confirm request that consumes it.
create table password_reset_tokens (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  user_id     uuid not null references users(id) on delete cascade,
  token_hash  text not null unique,
  expires_at  timestamptz not null,
  used_at     timestamptz,
  created_at  timestamptz not null default now()
);

create index password_reset_tokens_user_idx on password_reset_tokens (user_id);

alter table password_reset_tokens enable row level security;
alter table password_reset_tokens force row level security;

create policy tenant_isolation on password_reset_tokens
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
