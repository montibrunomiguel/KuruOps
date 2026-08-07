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
    execute format('drop policy if exists tenant_isolation on %I', t);
    execute format('alter table %I no force row level security', t);
    execute format('alter table %I disable row level security', t);
  end loop;
end $$;

drop function if exists current_tenant_id();
