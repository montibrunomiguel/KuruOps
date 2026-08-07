-- escalation_policies backs Settings -> On-Call Escalation: a self-contained
-- per-severity rule for notifying whoever's on shift (see on_call_shifts)
-- when an alert has gone unacknowledged too long. Deliberately decoupled
-- from incidents.sla_due_at/sla_breached (incident_sla_policies) -- this
-- fires on alert age + severity, nothing to do with an incident's SLA
-- clock, and applies before an alert ever becomes an incident.
create table escalation_policies (
  id                            uuid primary key default gen_random_uuid(),
  tenant_id                     uuid not null references tenants(id) on delete cascade,
  severity                      severity_enum not null,
  unacknowledged_after_minutes  integer not null check (unacknowledged_after_minutes > 0),
  channel_type                  text not null check (channel_type in ('pagerduty', 'slack', 'webhook')),
  destination_secret_ref        text not null,
  created_at                    timestamptz not null default now(),
  updated_at                    timestamptz not null default now(),
  unique (tenant_id, severity)
);

alter table escalation_policies enable row level security;
alter table escalation_policies force row level security;

create policy tenant_isolation on escalation_policies
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());

-- escalated_at marks the moment cmd/worker's escalation sweep fired a
-- notification for this alert -- distinct from alerts.status = 'escalated'
-- (an analyst's own manual status choice, see alert_status_enum): this
-- column only exists to stop the same alert re-notifying on every sweep
-- tick once a policy has already fired for it. Never cleared once set,
-- same "historical fact" convention as incidents.sla_breached.
alter table alerts add column escalated_at timestamptz;
