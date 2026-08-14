-- Lets an admin configure, per webhook endpoint, which JSON fields of an
-- incoming payload define "this is the same event" (e.g. host.name,
-- rule.id) -- see internal/service/alert_service.go's computeGroupKey.
-- group_by_fields empty (the default) means dedup is off for this endpoint,
-- same as field_mapping_template_id being null means no mapping applies.
-- dedup_window_minutes follows the same "configurable, sensible default"
-- shape as escalation_policies.unacknowledged_after_minutes.
alter table webhook_endpoints
  add column group_by_fields text[] not null default '{}',
  add column dedup_window_minutes integer not null default 30;

-- group_key is the canonical value computed from an alert's payload at
-- ingest time via the endpoint's group_by_fields (see computeGroupKey) --
-- null when dedup was off for that endpoint, or when the incoming payload
-- was missing one of the configured fields (see AlertService.Ingest's doc
-- comment: a missing field means "don't dedup this one", not "match on
-- absence"). duplicate_count is how many subsequent payloads within the
-- window matched this alert's group_key and were suppressed rather than
-- creating a new alert.
alter table alerts
  add column group_key text,
  add column duplicate_count integer not null default 0;

-- Partial index (only rows that actually participate in dedup) backing
-- AlertRepository.FindAndIncrementDuplicate's lookup: same
-- (webhook_endpoint_id, group_key) pair, most recent received_at first.
create index alerts_dedup_lookup_idx on alerts (webhook_endpoint_id, group_key, received_at)
  where group_key is not null;
