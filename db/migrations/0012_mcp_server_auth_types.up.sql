-- mcp_servers: replace the single implicit "optional bearer token" with an
-- explicit auth type -- none, api_key (custom header + key), bearer, or oauth
-- (client_credentials: token URL + client id + client secret).
--
-- Every secret (API key / bearer token / OAuth client secret) lives in
-- secrets.Store like every other integration's credential; Postgres only ever
-- holds an opaque *_secret_ref. auth_secret_ref (already here since 0001) is
-- reused for the two single-secret types, api_key and bearer -- an oauth row
-- has no use for it and carries oauth_client_secret_ref instead.
--
-- auth_type is text + CHECK, not a new native enum (see README.md in this
-- directory for why), and the shape CHECK makes the database itself refuse an
-- incoherent combination (an api_key row with no header, an oauth row with a
-- stray bearer ref, a "none" row still pointing at a secret) rather than
-- trusting every future writer to validate.
alter table mcp_servers
  add column auth_type text not null default 'none',
  add column auth_header_name text,
  add column oauth_token_url text,
  add column oauth_client_id text,
  add column oauth_client_secret_ref text;

-- Before this migration the only credential an MCP server could have was a
-- bearer token, stored in auth_secret_ref -- so every row that has one is,
-- by definition, a bearer row.
update mcp_servers set auth_type = 'bearer' where auth_secret_ref is not null;

alter table mcp_servers
  add constraint mcp_servers_auth_type_check
    check (auth_type in ('none', 'api_key', 'bearer', 'oauth')),
  add constraint mcp_servers_auth_shape_check check (
    (auth_type = 'none'
      and auth_secret_ref is null and auth_header_name is null
      and oauth_token_url is null and oauth_client_id is null and oauth_client_secret_ref is null)
    or (auth_type = 'api_key'
      and auth_secret_ref is not null and auth_header_name is not null
      and oauth_token_url is null and oauth_client_id is null and oauth_client_secret_ref is null)
    or (auth_type = 'bearer'
      and auth_secret_ref is not null and auth_header_name is null
      and oauth_token_url is null and oauth_client_id is null and oauth_client_secret_ref is null)
    or (auth_type = 'oauth'
      and auth_secret_ref is null and auth_header_name is null
      and oauth_token_url is not null and oauth_client_id is not null and oauth_client_secret_ref is not null)
  );
