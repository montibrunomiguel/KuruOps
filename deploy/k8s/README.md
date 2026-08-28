<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# KuruOps on Kubernetes

Basic manifests to run the real stack (api, ingest, worker, frontend) on a
cluster. Deliberately minimal: no Helm chart, no HorizontalPodAutoscaler, no
ingress controller or cert-manager integration. This gets a working
deployment onto a cluster; layering ingress/TLS/autoscaling on top is left
to whatever your organization already uses for that, rather than this
project picking one for you.

## What's NOT included

- **Postgres.** Every manifest here assumes an existing, reachable Postgres
  16 instance (a managed RDS/Cloud SQL instance, a StatefulSet you manage
  separately, etc.) — the same "customer-supplied database" assumption the
  app's own external-DB-migration Settings feature already makes. The
  `kuruops_app`/`kuruops_worker` roles and RLS policies must already exist
  there (see `db/init/*.sql` and `db/README.md`) before pointing
  `01-secret.example.yaml` at it.
- **Ingress/TLS.** `frontend`'s Service is `ClusterIP` — reaching it from
  outside the cluster (an ingress controller, a cloud LoadBalancer Service,
  `kubectl port-forward` for a quick look) is up to you.
- **Autoscaling.** No HorizontalPodAutoscaler — every replica count below is
  a fixed number you set by hand. `ingest` ships at `replicas: 2` by default
  (see its own comment): it has no local-disk dependency, and its rate
  limiter/SSE-adjacent infra are already Postgres-backed for exactly this.
  `api` ships at `replicas: 1` — its `uploads` PVC is `ReadWriteOnce`, so
  going beyond 1 replica needs either a ReadWriteMany StorageClass or
  switching Settings -> Storage to the S3/GCS backend first (see
  `04-api.yaml`'s comment for the exact steps). `worker` stays at 1 by
  design (its sweep jobs use a Postgres advisory lock specifically so
  running more than one replica is safe but not currently useful — only one
  would ever be doing work at a time).
- **Secrets management.** `01-secret.example.yaml` is a template with
  placeholder values, not something to `kubectl apply` and forget — see its
  own comments.

## Apply order

```bash
kubectl apply -f deploy/k8s/00-namespace.yaml

# Copy 01-secret.example.yaml, fill in every CHANGEME, do NOT commit the
# copy -- then apply your real one instead of the example file.
kubectl apply -f deploy/k8s/02-configmap.yaml

# Waits for the Job to actually finish before continuing -- api/ingest/worker
# all assume the schema already exists on first boot.
kubectl apply -f deploy/k8s/03-migration-job.yaml
kubectl wait --for=condition=complete --timeout=120s -n kuruops job/kuruops-migrate

kubectl apply -f deploy/k8s/04-api.yaml
kubectl apply -f deploy/k8s/05-ingest.yaml
kubectl apply -f deploy/k8s/06-worker.yaml
kubectl apply -f deploy/k8s/07-frontend.yaml

# Optional but strongly recommended before real data flows in -- see
# "Backups" below and 08-backup-cronjob.yaml's own comments for the S3
# bucket/IAM prerequisites this needs first.
kubectl apply -f deploy/k8s/08-backup-cronjob.yaml

# Optional but strongly recommended for an assisted-production launch -- see
# "Monitoring" below. No prerequisites beyond the cluster itself; the
# Alertmanager receiver ships as a placeholder null route until you edit
# 09-monitoring.yaml's alertmanager-config to point at a real destination.
kubectl apply -f deploy/k8s/09-monitoring.yaml
```

Re-running the whole set after a new release is safe: the migration Job is
idempotent (golang-migrate only applies versions it hasn't recorded yet, see
`03-migration-job.yaml`'s comment), and every Deployment/Service/ConfigMap
apply is a plain declarative update. Bump the `image:` tag in
`04-api.yaml`/`05-ingest.yaml`/`06-worker.yaml`/`07-frontend.yaml` to
whatever your build pushed (`:latest` here is a placeholder for trying the
manifests out, not a real release-pinning strategy).

## Images

Built the same way `docker-compose.yml` builds them locally
(`backend/Dockerfile`'s `api`/`ingest`/`worker` targets,
`frontend/Dockerfile`) — push them to whatever registry your cluster can
pull from and update the `image:` fields accordingly; these manifests
reference the same untagged local names `docker compose build` produces
(`kuruops-api:latest`, etc.) as a placeholder.

## Health checks and resource limits

`api`/`ingest`/`worker` all expose `/healthz` (pings the database, 503 on
failure — see `internal/httpserver.HealthCheck`), `/livez` (unconditional
200, no downstream dependency — see `internal/httpserver.Livez`), and
`/metrics` (Prometheus text format, see `internal/httpserver/metrics.go`).
`readinessProbe` uses `/healthz` and `livenessProbe` uses `/livez` —
deliberately different: a liveness probe backed by the same DB check as
readiness would make kubelet kill and restart every replica at once on a
transient Postgres blip, turning a brief DB hiccup into a synchronized
restart storm instead of just letting readiness route around it.
`resources.requests`/`limits` mirror
`docker-compose.yml`'s `mem_limit`/`cpus` for the same services — same
caveat applies: sized for trying this out, not a production sizing
recommendation. Watch the real `kuruops_db_pool_*` /
`kuruops_http_request_duration_seconds` series on `/metrics` once deployed
and adjust from there.

## Monitoring

`09-monitoring.yaml` runs a minimal self-hosted Prometheus + Alertmanager —
scraping api/ingest/worker's existing `/metrics` (no code changes needed,
it's already there) and alerting on the basics: a scrape target going down,
an elevated 5xx rate, high p99 latency, connection-pool saturation, and a
worker sweep job going stale (see the ConfigMap's own comments for exact
thresholds — starting points, not tuned against real traffic yet). No
persistent storage and no Grafana, deliberately: this answers "is something
on fire right now", not "show me a dashboard of the last 30 days". See the
manifest's own header comment for how to point a managed provider (Grafana
Cloud, Datadog, etc.) at the same metrics later instead. The Alertmanager
receiver ships as a placeholder null route — alerts fire and show up in
Prometheus/Alertmanager's own UI, but notify no one until you edit
`alertmanager-config` to point at a real destination.

## Backups

`08-backup-cronjob.yaml` runs `pg_dump` once a day and uploads the dump to
S3 — see its own comments for the exact prerequisites (bucket, IAM
credentials, the `BACKUP_*` keys it expects in `01-secret.example.yaml` and
`02-configmap.yaml`). RPO with the default daily schedule is ~24h; that's a
documented starting point for an assisted-production launch, not a
substitute for WAL archiving/point-in-time recovery if you need a tighter
RPO later. To restore, see `docs/OPERATIONS.md`'s restore runbook — the same
`pg_restore` path is exercised locally by `task db:backup:restore-test`
against a disposable database, so that's also the fastest way to confirm a
given dump is actually restorable before you ever need to rely on one for
real.
