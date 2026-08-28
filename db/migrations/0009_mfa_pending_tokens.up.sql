-- mfa_pending_tokens: the second, short-lived leg of a local login for a
-- user with TOTP enrolled (users.mfa_totp_secret set). LoginLocal verifies
-- the password and, instead of issuing a session, stops here: it mints one
-- of these rows and hands the caller only its plaintext, which the frontend
-- must then present alongside a 6-digit code to POST /auth/mfa/verify
-- before a real session token is ever issued. Same single-use/hashed/TTL'd
-- shape as oauth_states -- ConsumeByHash there is the pattern to copy
-- (atomic "update ... where consumed_at is null and expires_at > now()
-- returning ..." avoids the TOCTOU gap a separate get-then-mark-used pair
-- would leave open), not password_reset_tokens' older get/mark-used pair.
--
-- No `provider` column (unlike oauth_states) -- there's only ever one kind
-- of pending-token flow here. A short 10-minute TTL, not password reset's
-- 1-hour one: this only needs to survive the few seconds between a
-- password check succeeding and the user typing the code already showing
-- on their device.
create table mfa_pending_tokens (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  user_id     uuid not null references users(id) on delete cascade,
  token_hash  text not null,
  expires_at  timestamptz not null,
  consumed_at timestamptz,
  created_at  timestamptz not null default now(),
  constraint mfa_pending_tokens_token_hash_key unique (token_hash)
);

create index mfa_pending_tokens_user_idx on mfa_pending_tokens (user_id);

alter table mfa_pending_tokens enable row level security;
alter table mfa_pending_tokens force row level security;

create policy tenant_isolation on mfa_pending_tokens
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
