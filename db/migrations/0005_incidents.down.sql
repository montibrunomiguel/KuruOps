drop table if exists incident_comments;
drop table if exists incident_events;
drop table if exists incident_status_history;
drop table if exists incident_alert_links;
alter table alerts drop constraint if exists alerts_incident_id_fkey;
drop table if exists incidents;
