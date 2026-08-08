-- Durable backing store for secrets.PersistentEnvStore (see
-- backend/internal/secrets/persistent_store.go) -- the default "env"
-- backend previously kept every LDAP bind password / SAML SP keypair /
-- LLM API key / webhook secret in an in-memory map only, so a process
-- restart silently dropped all of them while the row referencing them
-- (via the ref string) kept pointing at the now-empty value. Values here
-- are AES-256-GCM ciphertext, encrypted with SECRETS_ENCRYPTION_KEY (never
-- stored in Postgres) -- a database dump/backup alone still doesn't hand
-- out a tenant's secrets, matching the guarantee the rest of this package
-- already promises.
--
-- No RLS: ref already encodes the tenant ID as a plain-text prefix
-- ("<tenant-uuid>:<purpose>"), and this table is never queried by tenant --
-- only ever by exact ref lookup or a full load at process startup. Same
-- "global, no per-tenant policy" shape as the tenants table itself.
create table secret_store (
    ref text primary key,
    nonce bytea not null,
    ciphertext bytea not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);
