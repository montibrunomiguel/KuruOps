alter table playbooks
  add column alert_name_pattern text not null default '',
  add column is_default boolean not null default false;

create unique index playbooks_one_default_per_tenant on playbooks (tenant_id) where is_default;

alter table playbook_phase_steps
  add column webhook_url text not null default '',
  add column webhook_payload_template text not null default '';

alter table alerts
  add column playbook_id uuid references playbooks(id) on delete set null;

create index alerts_playbook_id_idx on alerts (playbook_id) where playbook_id is not null;

-- Backfill existing alerts with whatever playbook would match them today:
-- a specific alert_name_pattern first (most specific pattern wins), else
-- whichever playbook is marked as the tenant's default.
update alerts a set playbook_id = (
  select p.id from playbooks p
  where p.tenant_id = a.tenant_id
    and (
      (p.alert_name_pattern <> '' and a.title ilike p.alert_name_pattern)
      or p.is_default
    )
  order by (p.alert_name_pattern <> '' and a.title ilike p.alert_name_pattern) desc, length(p.alert_name_pattern) desc
  limit 1
);
