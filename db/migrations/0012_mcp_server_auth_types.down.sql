-- Back to "optional bearer token only". A bearer row keeps its auth_secret_ref
-- and keeps working; api_key and oauth rows can't be represented in the old
-- shape, so they're downgraded to "no auth" -- an api_key row's ref in particular
-- must not survive, or it would be sent as "Authorization: Bearer <api key>".
update mcp_servers set auth_secret_ref = null where auth_type <> 'bearer';

alter table mcp_servers
  drop constraint mcp_servers_auth_shape_check,
  drop constraint mcp_servers_auth_type_check,
  drop column oauth_client_secret_ref,
  drop column oauth_client_id,
  drop column oauth_token_url,
  drop column auth_header_name,
  drop column auth_type;
