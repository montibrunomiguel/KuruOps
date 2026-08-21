-- Drops everything 0001_initial_schema.up.sql creates. This is a from-
-- scratch baseline, not a chain of incremental changes, so there is no
-- "previous state" to restore piece by piece the way every later
-- migration's down file does -- dropping and recreating the whole public
-- schema is simpler and equally correct here.
drop schema public cascade;
create schema public;
