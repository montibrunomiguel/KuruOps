-- Backs middleware.KeyedLimiter's sliding-window rate limiting (login by
-- IP, login by email, webhook ingestion by IP). Previously in-memory per
-- process, which meant N replicas of cmd/api or cmd/ingest multiplied the
-- effective limit by N instead of enforcing it coherently -- a shared
-- Postgres table fixes that without adding a new service (Redis) to the
-- deploy. Not tenant data (rate limiting happens before/outside any
-- tenant context, e.g. an unauthenticated login attempt), so no RLS here,
-- same as webhook_endpoints' token lookup path.
create table rate_limit_events (
  scope text not null,
  key text not null,
  occurred_at timestamptz not null default now()
);

-- Every KeyedLimiter.Allow call filters by (scope, key) then by
-- occurred_at -- this index covers both in one pass.
create index rate_limit_events_scope_key_occurred_at_idx on rate_limit_events (scope, key, occurred_at);
