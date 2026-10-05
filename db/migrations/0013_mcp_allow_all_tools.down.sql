-- A server that relied on allow_all_tools falls back to its allowed_tools list,
-- which may be empty -- that is, back to offering the agent nothing until an
-- admin picks tools again, the fail-safe direction.
alter table mcp_servers drop column allow_all_tools;
