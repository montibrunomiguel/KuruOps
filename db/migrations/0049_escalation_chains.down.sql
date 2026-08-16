-- Lossy: a chain with more than one step collapses back down to a single
-- row using its first step's (lowest position) config, and a chain with
-- zero steps falls back to a placeholder config that will need an admin's
-- attention -- same accepted irreversibility as other multi-row ->
-- single-row down migrations in this schema (e.g. 0047's).
alter table escalation_policies add column unacknowledged_after_minutes integer;
alter table escalation_policies add column channel_type text;
alter table escalation_policies add column destination_secret_ref text;
alter table escalation_policies add column webhook_payload_template text;

update escalation_policies ep
set unacknowledged_after_minutes = s.delay_minutes,
    channel_type = s.channel_type,
    destination_secret_ref = s.destination_secret_ref,
    webhook_payload_template = s.webhook_payload_template
from escalation_policy_steps s
where s.policy_id = ep.id
  and s.position = (select min(position) from escalation_policy_steps where policy_id = ep.id);

update escalation_policies set
  unacknowledged_after_minutes = coalesce(unacknowledged_after_minutes, 30),
  channel_type = coalesce(channel_type, 'webhook'),
  destination_secret_ref = coalesce(destination_secret_ref, '')
where unacknowledged_after_minutes is null or channel_type is null or destination_secret_ref is null;

alter table escalation_policies alter column unacknowledged_after_minutes set not null;
alter table escalation_policies add constraint escalation_policies_unacknowledged_after_minutes_check check (unacknowledged_after_minutes > 0);
alter table escalation_policies alter column channel_type set not null;
alter table escalation_policies add constraint escalation_policies_channel_type_check check (channel_type in ('pagerduty', 'slack', 'webhook'));
alter table escalation_policies alter column destination_secret_ref set not null;

drop table escalation_policy_steps;

alter table alerts drop column sla_escalation_step;
alter table alerts drop column manual_escalation_step;
