-- Forces a fresh default-admin credential (see 0013_seed_default_admin.up.sql)
-- to be rotated before the account can do anything else -- see
-- middleware.RequirePasswordChanged in the Go backend.
alter table users add column must_change_password boolean not null default false;
