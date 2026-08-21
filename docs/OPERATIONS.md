<p align="right"><a href="OPERATIONS.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Operations

Runbook for whoever is watching ArgusOps run (assisted production): what the healthchecks mean,
how to read the logs, how to roll back a bad deploy, how to restore from a backup. Don't confuse
this with `TROUBLESHOOTING.md` (development/CI gotchas) or `THREAT_MODEL.md` (secret rotation and
threat model) — this one is "what to do when something blinks on the dashboard," the other two
are about something else.

## Healthchecks

Three endpoints, each with a different purpose — see `backend/internal/httpserver/healthcheck.go`:

- **`GET /healthz`** — pings Postgres with a 2s timeout; 503 if it can't. Used as the
  `readinessProbe` (see `deploy/k8s/04-api.yaml` etc.): a pod that fails here is pulled out of the
  Service until it responds again, without being restarted.
- **`GET /livez`** — always 200, checks nothing. Used as the `livenessProbe` — deliberately
  independent from Postgres, so a transient database blip doesn't take down every replica at once
  (kubelet would kill and restart everyone simultaneously if liveness also depended on the
  database).
- **`GET /metrics`** — Prometheus exposition format, see the next section.

**If `/healthz` is failing**: the first suspect is Postgres itself (network, credentials,
`max_connections` exhausted) — not the pod. `kubectl logs` on the pod won't show much beyond
"couldn't ping it"; look at the database side instead (RDS/Cloud SQL metrics, or `docker compose
logs postgres` locally).

**If `/livez` is failing** (the pod doesn't even respond 200 to this): the process itself has
hung or died — that's when it's time to look at `kubectl logs`/`kubectl describe pod` for that
specific replica.

## Logs

Structured JSON via `log/slog` (not loose text) — every line carries a `request_id` field that
correlates all the lines from the same request, including across different handlers (see
`backend/internal/httpserver/middleware/logging.go`). To investigate a specific error:

1. Find the error line, grab the `request_id`.
2. Filter every line with that same `request_id` (in whatever log aggregator you use —
   `kubectl logs | grep` works for a quick local check).
3. That reconstructs the entire request, not just the line that failed.

## Rolling back a bad deploy

There's no blue-green or canary here — it's a real `kubectl rollout undo`:

```bash
kubectl rollout history deployment/argusops-api -n argusops
kubectl rollout undo deployment/argusops-api -n argusops
# repeat for argusops-ingest / argusops-worker / argusops-frontend if the bad deploy touched those too
```

A new migration (`deploy/k8s/03-migration-job.yaml`) is **not** automatically rolled back by
this — `kubectl rollout undo` only reverts the container image, not the database schema. If the
bad deploy included a migration that's incompatible with the previous app version, rolling back
the Deployment without also rolling back the migration can leave the old app running against a
schema it doesn't understand. Confirm the migration in question was additive (a new column/table,
not a rename/drop) before trusting the Deployment rollback alone.

## Restoring from a backup

`deploy/k8s/08-backup-cronjob.yaml` runs `pg_dump` once a day and uploads it to S3 — an RPO of
~24h (see the manifest's own comment). To restore:

1. Download the most recent dump from the configured S3 bucket (`aws s3 cp
   s3://$BACKUP_S3_BUCKET/argusops/<file>.dump .`).
2. **Never restore straight over the production database without validating the dump first** —
   run `task db:backup:restore-test` locally first (it points at the most recent dump in
   `backups/`, restores into a disposable `argusops_backup_verify` database, runs a sanity count
   on `tenants`/`alerts`/`incidents`, then tears down the disposable database). This never touches
   the real database — it's safe to run at any time to confirm a dump is actually restorable.
3. Only after it's validated, restore into the real database:
   ```bash
   pg_restore --no-owner --no-privileges -d <real DATABASE_URL> <file>.dump
   ```
4. This is a full restore (it replaces the current state) — not incremental. Any write made after
   the most recent daily backup is lost; that's exactly what "RPO of ~24h" means. If that's
   unacceptable for your case, WAL archiving/PITR (not implemented yet) is the next step, not this
   runbook.

## Reference queries (Prometheus)

`deploy/k8s/09-monitoring.yaml` brings up a minimal Prometheus (no Grafana, no persistent storage
— see the manifest's own comment) that already has the alerting rules that fire on their own. The
queries below are for manual investigation during the assisted window — paste them into
`http://<prometheus>:9090/graph`:

- **Error rate per service**: `sum by (job) (rate(argusops_http_requests_5xx_total[5m])) / sum by (job) (rate(argusops_http_requests_total[5m]))`
- **p99 latency per service**: `histogram_quantile(0.99, sum by (le, job) (rate(argusops_http_request_duration_seconds_bucket[5m])))`
- **Connection pool saturation**: `argusops_db_pool_acquired_conns / argusops_db_pool_max_conns`
- **Active SSE connections** (should vary with logged-in analysts, not grow unbounded): `argusops_sse_active_connections`
- **How long since each worker sweep last succeeded** (in minutes): `(time() - argusops_worker_last_sweep_success_timestamp) / 60`
- **Scrape targets that are down**: `up{job=~"argusops-.*"} == 0`

## Capacity baseline

First run of `task perf:smoke` (5 VUs, 30s) against the local stack via docker-compose
(development hardware, not a real production environment — use it as a relative reference, not an
absolute number):

- **`GET /api/v1/alerts` and `GET /api/v1/dashboard/stats`**: 100% success, ~9.5 req/s combined,
  p90 latency 18.7ms / p95 24.9ms / mean 16.3ms. Practically no visible bottleneck at this volume.
- **`POST /hooks` (webhook ingest)**: a genuine load-test finding, not a bug — under burst, ~59%
  of requests came back 429 because the `webhook_ip` rate limiter (`cmd/ingest/main.go`, 60
  requests/minute per source IP) kicked in on purpose. That's the real throughput limit for a
  single source (a single SIEM/IP) sending alerts: **~1 req/s sustained per source IP**. If a real
  integration needs more than that from a single source, the limit is hardcoded (not configurable
  via env today) — changing that is a separate security decision, not something to tweak in
  passing.

Re-run `task perf:load` (heavier, VUS/DURATION configurable) against the real environment before
the assisted launch and compare against the numbers above.

## Escalation / contacts

_Fill in with the team's real contacts before releasing to assisted production — who gets paged
when an Alertmanager alert fires, and over which channel (the same PagerDuty/Slack/webhook that
ArgusOps itself uses to escalate tenant security incidents, or a separate channel for platform
incidents)._
