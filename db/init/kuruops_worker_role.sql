-- Creates the role cmd/worker connects as to refresh the dashboard's
-- materialized views and sweep overdue incidents.sla_breached. Distinct
-- from kuruops_app (see kuruops_app_role.sql) and deliberately granted
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
-- to kuruops_app to fix a "permission denied to refresh" error; that made
-- REFRESH runnable but, per the above, also made its query run under
-- kuruops_app's (nobypassrls) RLS context -- so it silently refreshes to
-- an empty view every time instead of erroring, which is exactly why
-- MTTA/MTTR stayed "--/--" even after that fix. This role is the actual
-- fix: own the views with something that bypasses RLS, instead of
-- widening kuruops_app's own bypass (which is used for the live API/ingest
-- request path and must keep RLS as a real backstop there).
--
-- Single-purpose, minimally-privileged for what it actually does: SELECT
-- everywhere (for the view refresh and for reading incidents to sweep),
-- plus the one narrow UPDATE this role's second job needs -- see below.
-- Don't reuse this role for anything besides owning + refreshing the two
-- materialized views and sweeping SLA breaches -- BYPASSRLS is an unusually
-- broad grant and its blast radius should stay as narrow as these two jobs.
\set ON_ERROR_STOP off
create role kuruops_worker with login nosuperuser nocreatedb nocreaterole bypassrls;
\set ON_ERROR_STOP on

alter role kuruops_worker with password :'worker_password';

grant usage on schema public to kuruops_worker;
grant select on all tables in schema public to kuruops_worker;
alter default privileges in schema public grant select on tables to kuruops_worker;

-- cmd/worker's second job, sweepSLABreaches, runs the same cross-tenant,
-- no-WithTenant pattern as refreshMaterializedViews (see that function's own
-- doc comment) to flip incidents.sla_breached once sla_due_at has passed --
-- it needs UPDATE on incidents for exactly that, and only that column set
-- (sla_breached, updated_at) is ever written by this role.
grant update (sla_breached, updated_at) on incidents to kuruops_worker;

-- cmd/worker's third job, sweepEscalations, advances an overdue alert's
-- automatic SLA escalation loop each time a chain step fires:
-- sla_escalation_step (which step to fire next, wrapping around once the
-- chain is exhausted) and escalated_at (now redefined as "when the last
-- automatic step fired", not just a one-time stamp) -- same
-- column-scoped-UPDATE reasoning as the incidents grant above.
-- manual_escalation_step is deliberately NOT granted here -- it's only ever
-- written by AlertHandlers.escalate via the kuruops_app role (already has
-- full table grants), never by this worker's automatic sweep.
grant update (escalated_at, sla_escalation_step) on alerts to kuruops_worker;

-- cmd/worker's fourth job, sweepStaleAIRuns, fails any ai_analysis_runs row
-- still stuck 'running'/'paused' long past when it should have finished (a
-- pod killed mid-analysis leaves nothing else to ever transition it) --
-- same column-scoped-UPDATE reasoning as the grants above.
grant update (status, error, updated_at) on ai_analysis_runs to kuruops_worker;

-- cmd/worker's fifth job, sweepDataRetention, permanently deletes closed
-- alerts/incidents once their tenant's configured retention period
-- (Settings -> Retention, tenant_retention_config; 18 months if
-- unconfigured) has elapsed since they closed. Unlike the column-scoped
-- UPDATE grants above, this needs real DELETE -- the first job that
-- actually removes rows rather than just flipping a column. Granted on
-- alerts/incidents themselves, on ai_analysis_runs/ai_tool_calls (no FK/
-- cascade back to alerts/incidents -- context_type/context_id is a
-- polymorphic reference with no foreign key, see
-- db/migrations/0001_initial_schema.up.sql, so the sweep deletes these
-- explicitly rather than relying on a cascade), and on every child table
-- alerts/incidents cascade into -- Postgres still checks DELETE privilege
-- on the table a cascade actually removes rows from, not just the table
-- named in the original DELETE statement.
grant delete on alerts, incidents, ai_analysis_runs, ai_tool_calls,
  alert_comments, alert_events, alert_links,
  incident_alert_links, incident_assignees, incident_comments,
  incident_events, incident_role_assignments, incident_status_history
  to kuruops_worker;
-- Deleting an incident SETs NULL any still-open alert's incident_id
-- (alerts_incident_id_fkey ON DELETE SET NULL) -- implemented as a real
-- UPDATE against alerts, checked the same as any other UPDATE, so this
-- needs its own grant just like sla_escalation_step/escalated_at above.
grant update (incident_id) on alerts to kuruops_worker;

alter materialized view mv_alert_daily_stats owner to kuruops_worker;
alter materialized view mv_incident_kpis owner to kuruops_worker;
alter materialized view mv_incident_daily_stats owner to kuruops_worker;

-- The API (kuruops_app) still needs to read these for the dashboard --
-- only the owner (this role) can REFRESH them, but ownership doesn't imply
-- SELECT for anyone else, and moving ownership away from kuruops_app drops
-- whatever implicit access it had as the previous owner.
grant select on mv_alert_daily_stats, mv_incident_kpis, mv_incident_daily_stats to kuruops_app;
