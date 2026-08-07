-- Creates the least-privilege role the application connects as (see
-- db/README.md, "Role da aplicação"). Idempotent: safe to run every deploy,
-- not just the first one. Run AFTER migrations, not before -- the grants on
-- "all tables in schema public" only pick up tables that already exist.
--
-- NOTE: psql's `:'var'` client-side interpolation does NOT happen inside a
-- dollar-quoted string (a `do $$ ... $$` body is exactly that) -- psql's
-- tokenizer treats it as opaque, the same way the server's SQL lexer does.
-- So this can't be a single `do $$ if not exists ... $$` block with
-- `:'app_password'` inside it (that sends the literal text `:'app_password'`
-- to the server -- a syntax error). Instead: let CREATE ROLE fail harmlessly
-- if the role already exists, then unconditionally ALTER ROLE ... PASSWORD,
-- both as plain top-level statements where interpolation works normally.
\set ON_ERROR_STOP off
create role argusops_app with login nosuperuser nocreatedb nocreaterole nobypassrls;
\set ON_ERROR_STOP on

alter role argusops_app with password :'app_password';

grant usage on schema public to argusops_app;
grant select, insert, update, delete on all tables in schema public to argusops_app;
grant usage, select on all sequences in schema public to argusops_app;

-- Make the grants apply to tables created by future migrations too, so
-- `task db:migrate` after this step never needs a manual re-grant.
alter default privileges in schema public grant select, insert, update, delete on tables to argusops_app;
alter default privileges in schema public grant usage, select on sequences to argusops_app;

-- mv_alert_daily_stats/mv_incident_kpis are NOT owned by argusops_app --
-- see db/init/argusops_worker_role.sql for why (a materialized view's
-- defining query runs with its OWNER's RLS context, and these two need to
-- see across all tenants when cmd/worker refreshes them).
