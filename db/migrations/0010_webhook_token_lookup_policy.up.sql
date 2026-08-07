-- The ingest service authenticates a webhook delivery by token BEFORE it
-- knows which tenant sent it -- that's the one legitimate case where a
-- lookup has to run without app.tenant_id set. Rather than bypass RLS
-- entirely for that connection, add a second permissive SELECT policy that
-- only opens up when the ingest service explicitly sets app.webhook_lookup,
-- and only on this table. Multiple permissive policies for the same command
-- are OR'd by Postgres, so tenant-scoped access through tenant_isolation is
-- unaffected. The actual authentication boundary is still the random,
-- hashed token in the WHERE clause -- this policy only lets the query run
-- at all, it does not let it list rows.
create policy webhook_token_lookup on webhook_endpoints
  for select
  using (current_setting('app.webhook_lookup', true) = 'true');
