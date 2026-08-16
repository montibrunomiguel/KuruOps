-- escalation_policies becomes the "header" of an ordered chain (still one
-- per severity per tenant, see the existing unique(tenant_id, severity));
-- the trigger fields that used to live directly on it (when to fire, which
-- channel, where to) move per-step into escalation_policy_steps, so a
-- severity can escalate through multiple on-call schedules in sequence
-- instead of firing a single fixed notification.
alter table escalation_policies drop column unacknowledged_after_minutes;
alter table escalation_policies drop column channel_type;
alter table escalation_policies drop column destination_secret_ref;
alter table escalation_policies drop column webhook_payload_template;

create table escalation_policy_steps (
  id                       uuid primary key default gen_random_uuid(),
  policy_id                uuid not null references escalation_policies(id) on delete cascade,
  tenant_id                uuid not null references tenants(id) on delete cascade,
  position                 integer not null,
  schedule_id              uuid not null references on_call_schedules(id),
  -- Minutes after the previous event (the alert opening, for position 0;
  -- the previous step's fire time, for later positions) before this step
  -- fires -- "a cada X tempo uma nova escala é acionada."
  delay_minutes            integer not null check (delay_minutes > 0),
  channel_type             text not null check (channel_type in ('pagerduty', 'slack', 'webhook')),
  destination_secret_ref   text not null,
  webhook_payload_template text,
  created_at               timestamptz not null default now(),
  updated_at               timestamptz not null default now(),
  unique (policy_id, position)
);

alter table escalation_policy_steps enable row level security;
alter table escalation_policy_steps force row level security;

create policy tenant_isolation on escalation_policy_steps
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());

-- Two independent per-alert counters (confirmed with the user during
-- design): the automatic SLA loop and a human manually escalating an alert
-- do NOT share "which step are we on" -- see cmd/worker's sweepEscalations
-- (advances sla_escalation_step, wraps around) and AlertHandlers.escalate
-- (advances manual_escalation_step by exactly one, capped at chain length,
-- never wraps).
alter table alerts add column sla_escalation_step integer not null default 0;
alter table alerts add column manual_escalation_step integer not null default 0;

-- escalated_at (added in 0029) now means "when the last automatic SLA-loop
-- step fired", updated on every loop iteration rather than stamped once --
-- confirmed safe to redefine: it's read nowhere outside cmd/worker and its
-- own tests.
