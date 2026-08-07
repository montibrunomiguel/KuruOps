-- Tenant isolation is enforced in the database, not only by application
-- query filters. Every request sets `app.tenant_id` for the duration of the
-- transaction (see internal/db.WithTenant in the Go backend); RLS makes it
-- impossible for a missed WHERE clause to leak another tenant's rows.
--
-- IMPORTANT: RLS is bypassed by table owners and superusers by default.
-- The application must connect as a non-superuser role that does NOT own
-- these tables (e.g. `argusops_app`), or policies below have no effect.
-- See db/README.md for the role setup.

create or replace function current_tenant_id() returns uuid as $$
  select nullif(current_setting('app.tenant_id', true), '')::uuid
$$ language sql stable;

do $$
declare
  t text;
begin
  foreach t in array array[
    'users', 'auth_group_mappings',
    'alerts', 'alert_links', 'alert_events',
    'incidents', 'incident_alert_links', 'incident_status_history',
    'incident_events', 'incident_comments',
    'playbooks', 'playbook_phase_steps', 'webhook_endpoints',
    'llm_providers', 'mcp_servers', 'ai_tool_calls'
  ]
  loop
    execute format('alter table %I enable row level security', t);
    execute format('alter table %I force row level security', t);
    execute format(
      'create policy tenant_isolation on %I using (tenant_id = current_tenant_id()) with check (tenant_id = current_tenant_id())',
      t
    );
  end loop;
end $$;

-- tenants itself has no tenant_id column; membership is implied by row
-- existence, and it is only ever queried by id from trusted backend code
-- during login/provisioning -- not part of the per-request RLS boundary.
