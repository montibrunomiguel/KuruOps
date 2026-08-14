drop index if exists alerts_playbook_id_idx;
alter table alerts drop column if exists playbook_id;
alter table playbook_phase_steps drop column if exists webhook_payload_template;
alter table playbook_phase_steps drop column if exists webhook_url;
drop index if exists playbooks_one_default_per_tenant;
alter table playbooks drop column if exists is_default;
alter table playbooks drop column if exists alert_name_pattern;
