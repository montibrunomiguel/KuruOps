alter table alerts drop constraint if exists alerts_webhook_endpoint_id_fkey;
drop table if exists webhook_endpoints;
drop table if exists playbook_phase_steps;
drop table if exists playbooks;
