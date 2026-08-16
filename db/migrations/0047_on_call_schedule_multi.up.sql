-- Settings -> On-Call Schedule moves from "exactly one rotation per tenant"
-- to "any number of named rotations per tenant" (e.g. one per team/product),
-- with exactly one marked as the default -- OnCallScheduleService's hot
-- resolution path (ResolveCurrentAnalyst, consulted by AlertService.Ingest
-- and cmd/worker's escalation sweep) always resolves against the default,
-- never "pick one arbitrarily". See PlaybookRepository/LLMProviderRepository
-- for the same "collection + one default per tenant" shape this mirrors.
alter table on_call_schedules drop constraint on_call_schedules_tenant_id_key;

alter table on_call_schedules add column is_default boolean not null default false;

-- Backfill: under the old unique(tenant_id) constraint there was at most one
-- row per tenant, so making every existing row its tenant's default
-- preserves today's alert-assignment behavior exactly across this migration.
update on_call_schedules set is_default = true;

create unique index on_call_schedules_one_default_per_tenant on on_call_schedules (tenant_id) where is_default;
