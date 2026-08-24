drop index if exists alerts_search_vector_gin;
alter table alerts drop column if exists search_vector;

drop index if exists incidents_search_vector_gin;
alter table incidents drop column if exists search_vector;
