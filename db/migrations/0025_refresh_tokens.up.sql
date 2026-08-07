-- refresh_tokens backs the long-lived side of the auth pair: the JWT
-- access token stays 15 minutes and stateless (no DB round-trip to verify
-- it -- see internal/authn/jwt.go), but that alone gave the app no way to
-- end a session early. A refresh token here is the revocation point:
-- deactivating a user or hitting "Revoke sessions" (AuthService.RevokeSessions)
-- marks every row for that user revoked, which POST /auth/refresh checks
-- before issuing a new access token. Hashed at rest (sha256) and rotated
-- on every use, same convention as webhook_endpoints.token_hash
-- (WebhookService's generateToken/hashToken) -- the plaintext token only
-- ever lives in the client's storage and the one response that issued it.
create table refresh_tokens (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  user_id     uuid not null references users(id) on delete cascade,
  token_hash  text not null unique,
  expires_at  timestamptz not null,
  revoked_at  timestamptz,
  created_at  timestamptz not null default now()
);

create index refresh_tokens_user_idx on refresh_tokens (user_id);

alter table refresh_tokens enable row level security;
alter table refresh_tokens force row level security;

create policy tenant_isolation on refresh_tokens
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
