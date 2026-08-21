<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# ArgusOps — database

Migrations in `migrations/`, `golang-migrate` format (`{version}_{name}.up.sql` / `.down.sql`).

## Local setup

Via Task (recommended — see the root `README.md`): `task db:up && task db:migrate && task db:roles`
does all of this, including the application role below, against the Postgres from
`docker-compose.yml`.

Manual, against an already-running Postgres:

```bash
createdb argusops
migrate -database "postgres://localhost:5432/argusops?sslmode=disable" -path migrations up
```

Without `golang-migrate` installed:
`go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`

## Application role (required before running `cmd/api` / `cmd/ingest` / `cmd/worker`)

The RLS policies in `0001_initial_schema.up.sql`
only protect the data if the application connects with a role that is **not** the owner of the
tables and does **not** have `BYPASSRLS`. Running the migrations as a superuser/owner and then
connecting the application with that same user makes RLS harmless — the table owner bypasses
policies by default.

`task db:roles` runs exactly this (`db/init/argusops_app_role.sql`, idempotent — safe to run again
on every deploy). Manual equivalent:

```sql
create role argusops_app with login password '...' nosuperuser nocreatedb nocreaterole nobypassrls;
grant usage on schema public to argusops_app;
grant select, insert, update, delete on all tables in schema public to argusops_app;
grant usage, select on all sequences in schema public to argusops_app;
```

The backend's `DATABASE_URL` must point to `argusops_app`, not to the table-owning user used to
run the migrations.

## Worker role (`cmd/worker`, materialized view refresh)

`cmd/worker` does **not** connect as `argusops_app` — it connects as `argusops_worker`
(`db/init/argusops_worker_role.sql`, also created by `task db:roles`), which has `BYPASSRLS`.

Reason: `REFRESH MATERIALIZED VIEW` can only be run by the view's owner, and a materialized view
runs its query with the **owner's** privileges, not the caller's (same rule as regular views).
`mv_alert_daily_stats`/`mv_incident_kpis`/`mv_incident_daily_stats` are cross-tenant aggregates by
definition (grouped by `tenant_id`, with no single tenant), and the refresh runs outside of any
tenant context — so if the view's owner is a role without `BYPASSRLS`, the `alerts`/`incidents`
policy `tenant_id = current_tenant_id()` never matches (no tenant is set), and the REFRESH
"succeeds" but always recomputes to zero rows, with no error at all. `argusops_worker` exists for
this — mostly just `SELECT`, with two narrow `UPDATE` exceptions restricted to one column each
(`incidents.sla_breached` and `alerts.escalated_at`, for the periodic sweeps that also run under
this role, see `db/init/argusops_worker_role.sql`) — and shouldn't be reused for anything beyond
these specific responsibilities.

## Why RLS and not just application-level filtering

The prototype's tag-based access model (`allowedTags` / `resourceAccess`) already points the right
way, but if it stays only at the application layer, a new query without `WHERE tenant_id = ...`
leaks another customer's data. The policies in `0001_initial_schema.up.sql` close off this
class of bug at the database level: every sensitive table only returns rows for the tenant set via
`select set_config('app.tenant_id', ...)` in the transaction (see `internal/db.Pool.WithTenant` in
the Go backend).

## Known limitation: materialized views and RLS

Postgres doesn't support RLS on materialized views. `mv_alert_daily_stats`, `mv_incident_kpis`, and
`mv_incident_daily_stats` (all in `0001_initial_schema.up.sql`) are cross-tenant aggregations by
definition — every query against them at the API layer **must** include `where tenant_id = $1`
manually. This is a documented exception to the "isolation in the database, not in the query"
principle, not an oversight.
