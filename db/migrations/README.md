# Migration conventions

Sequentially numbered `NNNN_description.up.sql` / `.down.sql` pairs, run in
order by `golang-migrate` (see `Taskfile.yml`'s `db:migrate`). Once a
migration has shipped, treat its `.up.sql`/`.down.sql` as historical record —
don't edit it; land a new migration instead, even to fix something in it.

`0001_initial_schema.up.sql` is the whole schema as of 2026-08-21, squashed
from the 50 incremental migrations that built it up over the course of
development (the project had no versioned release yet, so there was no
deployment anywhere that needed the old step-by-step history replayed —
see git history before this squash if that per-change rationale is ever
needed). `0002_seed_default_admin.up.sql` seeds the one tenant + admin
account every fresh deploy needs, kept separate from the schema itself
so `internal/dbmigrate`'s external-database-migration feature can cleanly
undo just the seed (see `Service.clearSeedData`) before copying a real
customer's data over. Every migration from here on is a normal incremental
one, landed on top — this file only describes conventions, not "the current
list of everything that happened."

A few conventions worth keeping going forward:

- **Status/kind columns: `text` + a `check` constraint, not a native
  `enum` type.** A few genuinely fixed, small vocabularies (`severity`,
  `incident_phase`, `auth_provider`, etc.) do use real Postgres enums —
  see `0001_initial_schema.up.sql`'s "Enum types" section. Every
  status/kind/type-ish column added since partway through this project's
  history uses `text not null check (col in (...))` instead: adding a
  value to a native enum can't run inside the same transaction as other
  schema changes, which `golang-migrate` needs. Keep doing that for new
  columns; don't reach for `create type ... as enum` again.

- **`references users(id)` defaults to RESTRICT on purpose.** A handful of
  tables (`refresh_tokens`, `password_reset_tokens`, `user_api_tokens`) use
  `on delete cascade` because they're ephemeral session/credential
  artifacts tied 1:1 to their user. Everything else that references a user
  as an assignee/author/commander/participant (`alerts`, `incidents`,
  `incident_assignees`, `incident_role_assignments`,
  `on_call_rotation_participants`, etc.) leaves the default RESTRICT: that
  data is meaningful authorship/audit history, not something that should
  silently vanish or cascade away if a user row is ever deleted.
  `users.is_active` exists specifically so deactivating a user never needs
  a hard delete — a hard `delete from users` is not expected to succeed
  once that user has any history, and that's intentional, not a bug to fix.

- **Materialized views can't carry RLS.** `mv_alert_daily_stats`,
  `mv_incident_kpis`, and `mv_incident_daily_stats` (all in
  `0001_initial_schema.up.sql`) key on `tenant_id`, but Postgres does not
  support `enable row level security` on a materialized view. Every query
  against one of these three MUST filter `where tenant_id = $1` explicitly
  — there is no RLS backstop for a missed filter here, unlike every other
  table in this schema. See `DashboardRepository.Stats`'s doc comment and
  `cmd/worker/main.go`'s `refreshMaterializedViews` for where this is
  currently handled correctly; if a new materialized view is added, it
  needs the same explicit-filter discipline at every call site.
