-- Dashboard aggregates computed server-side and refreshed periodically
-- (see cmd/worker), instead of recomputed client-side on every page load.
-- Matches the definitions in the design handoff:
--   MTTA = avg(acknowledged_at - received_at) over alerts that left 'open'
--   MTTR = avg(closed_at - received_at) over closed alerts
create materialized view mv_alert_daily_stats as
select
  tenant_id,
  date_trunc('day', received_at) as day,
  count(*) as alert_count,
  count(*) filter (where severity = 'critical') as critical_count,
  avg(extract(epoch from (acknowledged_at - received_at)))
    filter (where acknowledged_at is not null) as avg_mtta_seconds,
  avg(extract(epoch from (closed_at - received_at)))
    filter (where closed_at is not null) as avg_mttr_seconds
from alerts
group by tenant_id, date_trunc('day', received_at);

create unique index mv_alert_daily_stats_uq on mv_alert_daily_stats (tenant_id, day);

create materialized view mv_incident_kpis as
select
  tenant_id,
  count(*) filter (where phase <> 'post_incident') as active_incidents,
  count(*) filter (where sla_breached) as sla_breached_count,
  count(*) filter (where priority = 'p1' and phase <> 'post_incident') as p1_open_count,
  avg(extract(epoch from (
    (select min(coalesce(h.corrected_entered_at, h.entered_at)) from incident_status_history h
     where h.incident_id = i.id and h.phase = 'detection_analysis') - i.opened_at
  ))) as avg_mtta_seconds,
  avg(extract(epoch from (
    (select min(coalesce(h.corrected_entered_at, h.entered_at)) from incident_status_history h
     where h.incident_id = i.id and h.phase = 'post_incident') - i.opened_at
  ))) as avg_mttr_seconds
from incidents i
group by tenant_id;

create unique index mv_incident_kpis_uq on mv_incident_kpis (tenant_id);

-- Refreshed CONCURRENTLY on a schedule by the worker service; the unique
-- indexes above are required for CONCURRENTLY refresh to be usable.
