-- Day-level incident volume for the Dashboard's Incidents tab -- the
-- incident-side counterpart of mv_alert_daily_stats (0009). mv_incident_kpis
-- is tenant-wide only (no day dimension), so it can't back a "volume per
-- day" chart; a new view is simpler than restructuring that one.
create materialized view mv_incident_daily_stats as
select
  tenant_id,
  date_trunc('day', opened_at) as day,
  count(*) as incident_count
from incidents
group by tenant_id, date_trunc('day', opened_at);

create unique index mv_incident_daily_stats_uq on mv_incident_daily_stats (tenant_id, day);

-- Refreshed CONCURRENTLY on the same schedule as the other materialized
-- views (see cmd/worker/main.go's refreshMaterializedViews); the unique
-- index above is required for CONCURRENTLY refresh to be usable.
