drop index if exists alerts_dedup_lookup_idx;
alter table alerts drop column if exists duplicate_count;
alter table alerts drop column if exists group_key;
alter table webhook_endpoints drop column if exists dedup_window_minutes;
alter table webhook_endpoints drop column if exists group_by_fields;
