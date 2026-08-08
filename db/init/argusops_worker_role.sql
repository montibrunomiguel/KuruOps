-- Creates the role cmd/worker connects as to refresh the dashboard's
-- materialized views and sweep overdue incidents.sla_breached. Distinct
-- from argusops_app (see argusops_app_role.sql) and deliberately granted
-- BYPASSRLS.
--
-- Why this role has to exist and has to bypass RLS: Postgres executes a
-- materialized view's defining query with the VIEW OWNER's privileges, not
-- the privileges of whoever issues REFRESH (same rule as plain views) --
-- and REFRESH MATERIALIZED VIEW can only be run by the view's owner in the
-- first place (there's no separate grantable "refresh" privilege). So
-- whichever role owns mv_alert_daily_stats/mv_incident_kpis is both the
-- only role allowed to refresh them AND the role whose RLS context the
-- refresh's underlying query runs under.
--
-- Those two views are cross-tenant aggregates by design (grouped by
-- tenant_id, not scoped to one -- see cmd/worker/main.go's
-- refreshMaterializedViews, which intentionally runs outside
-- Pool.WithTenant), so nothing sets `app.tenant_id` during a refresh.
-- alerts/incidents both have `tenant_id = current_tenant_id()` as a FORCE
-- ROW LEVEL SECURITY policy, and current_tenant_id() reads that same
-- session setting -- so with no tenant context, it's null, and the policy
-- silently matches zero rows. Earlier this repo transferred view ownership
-- to argusops_app to fix a "permission denied to refresh" error; that made
-- REFRESH runnable but, per the above, also made its query run under
-- argusops_app's (nobypassrls) RLS context -- so it silently refreshes to
-- an empty view every time instead of erroring, which is exactly why
-- MTTA/MTTR stayed "--/--" even after that fix. This role is the actual
-- fix: own the views with something that bypasses RLS, instead of
-- widening argusops_app's own bypass (which is used for the live API/ingest
-- request path and must keep RLS as a real backstop there).
--
-- Single-purpose, minimally-privileged for what it actually does: SELECT
-- everywhere (for the view refresh and for reading incidents to sweep),
-- plus the one narrow UPDATE this role's second job needs -- see below.
-- Don't reuse this role for anything besides owning + refreshing the two
-- materialized views and sweeping SLA breaches -- BYPASSRLS is an unusually
-- broad grant and its blast radius should stay as narrow as these two jobs.
\set ON_ERROR_STOP off
create role argusops_worker with login nosuperuser nocreatedb nocreaterole bypassrls;
\set ON_ERROR_STOP on

alter role argusops_worker with password :'worker_password';

grant usage on schema public to argusops_worker;
grant select on all tables in schema public to argusops_worker;
alter default privileges in schema public grant select on tables to argusops_worker;

-- cmd/worker's second job, sweepSLABreaches, runs the same cross-tenant,
-- no-WithTenant pattern as refreshMaterializedViews (see that function's own
-- doc comment) to flip incidents.sla_breached once sla_due_at has passed --
-- it needs UPDATE on incidents for exactly that, and only that column set
-- (sla_breached, updated_at) is ever written by this role.
grant update (sla_breached, updated_at) on incidents to argusops_worker;

-- cmd/worker's third job, sweepEscalations, stamps alerts.escalated_at once
-- an on-call notification has fired for an overdue alert -- same
-- column-scoped-UPDATE reasoning as the incidents grant above.
grant update (escalated_at) on alerts to argusops_worker;

alter materialized view mv_alert_daily_stats owner to argusops_worker;
alter materialized view mv_incident_kpis owner to argusops_worker;
alter materialized view mv_incident_daily_stats owner to argusops_worker;

-- The API (argusops_app) still needs to read these for the dashboard --
-- only the owner (this role) can REFRESH them, but ownership doesn't imply
-- SELECT for anyone else, and moving ownership away from argusops_app drops
-- whatever implicit access it had as the previous owner.
grant select on mv_alert_daily_stats, mv_incident_kpis, mv_incident_daily_stats to argusops_app;
