-- tenant_retention_config: Settings -> Data & Audit -> Retention. How long
-- a CLOSED alert/incident stays in the tool after its closed_at, before
-- cmd/worker's sweepDataRetention hard-deletes it. Separate columns for
-- alerts vs incidents (the user explicitly wants independently configurable
-- periods, not one shared value).
--
-- Unlike tenant_smtp_config/tenant_slack_config (no row = feature OFF), no
-- row here means "the 18-month default applies" -- retention is on by
-- default. See RetentionConfigService.Get for where that default is
-- synthesized, and domain.DefaultRetentionMonths for the single source of
-- truth for "18" (also what cmd/worker's sweep SQL falls back to via
-- COALESCE for a tenant with no row yet).
--
-- >= 0 (not > 0) deliberately allows 0 months -- lets an admin (or a test)
-- verify the sweep purges effectively immediately.
create table tenant_retention_config (
  tenant_id uuid primary key references tenants(id) on delete cascade,
  alert_retention_months integer not null default 18,
  incident_retention_months integer not null default 18,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  constraint tenant_retention_config_alert_months_check check (alert_retention_months >= 0),
  constraint tenant_retention_config_incident_months_check check (incident_retention_months >= 0)
);

alter table tenant_retention_config enable row level security;
alter table tenant_retention_config force row level security;

create policy tenant_isolation on tenant_retention_config
  using (tenant_id = current_tenant_id())
  with check (tenant_id = current_tenant_id());
