-- incident_alert_links' only index today is its composite primary key
-- (incident_id, alert_id) -- unusable for a lookup keyed by alert_id alone,
-- which is exactly the access pattern AlertRepository now needs for every
-- alert row's computed incident_id (a correlated subquery) and for the
-- Correlated filter's exists/not-exists check (see alert_repository.go).
create index incident_alert_links_alert_id_idx on incident_alert_links (alert_id);
