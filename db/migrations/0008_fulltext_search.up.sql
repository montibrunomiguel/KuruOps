-- Full-text search on alerts/incidents: a generated tsvector column per
-- table, kept in sync automatically by Postgres on every insert/update (no
-- application-side maintenance, unlike a trigger-based approach), backed by
-- a GIN index so ListAlertsFilter/ListIncidentsFilter's new `q` param can
-- filter with `@@ plainto_tsquery(...)` instead of a slow ILIKE scan.
--
-- Weighted so a match in the title ranks above a match in a secondary
-- field -- not used for ordering yet (List still orders by received_at/
-- opened_at desc), but the weighting is free to add now and avoids a
-- second migration if ranked search is wanted later.
alter table alerts add column search_vector tsvector generated always as (
  setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
  setweight(to_tsvector('english', coalesce(source, '')), 'B') ||
  setweight(to_tsvector('english', coalesce(rule_id, '')), 'C') ||
  setweight(to_tsvector('english', coalesce(asset, '')), 'C')
) stored;

create index alerts_search_vector_gin on alerts using gin (search_vector);

alter table incidents add column search_vector tsvector generated always as (
  setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
  setweight(to_tsvector('english', coalesce(description, '')), 'B')
) stored;

create index incidents_search_vector_gin on incidents using gin (search_vector);
