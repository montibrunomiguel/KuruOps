-- Only safe if at most one schedule exists per tenant at rollback time (the
-- shape this table had before this migration) -- if it fails, delete the
-- extra schedules per tenant by hand first (or decide which one keeps
-- today's alert-assignment history) before retrying.
drop index if exists on_call_schedules_one_default_per_tenant;
alter table on_call_schedules drop column is_default;
alter table on_call_schedules add constraint on_call_schedules_tenant_id_key unique (tenant_id);
