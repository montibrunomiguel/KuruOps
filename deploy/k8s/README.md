<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# KuruOps on Kubernetes

Manifests to run the real stack (api, ingest, analyzer, worker, frontend) on a cluster. No Helm
chart: plain YAML you can read, diff and apply. It is exercised end to end -- migrations, roles,
all five images, NetworkPolicy, PodDisruptionBudgets, HorizontalPodAutoscalers, an Ingress with TLS,
and the webhook → ingest → analyzer → model pipeline across pods -- on a disposable kind cluster by
`scripts/k8s-e2e.sh`, which CI runs on every change.

## What's NOT included

- **Postgres.** Every manifest here assumes an existing, reachable Postgres 16 instance (a managed
  multi-AZ RDS/Cloud SQL instance is the best choice, or the optional 3-instance CloudNativePG
  cluster in [`optional/`](optional/README.md), which has automatic failover). The
  `kuruops_app`/`kuruops_worker` roles and RLS policies must already exist there (see
  `db/init/*.sql` and `db/README.md`) before pointing `01-secret.example.yaml` at it. Postgres is the
  one component these manifests do not make highly available themselves.
- **A certificate or DNS.** `12-ingress.yaml` is the public entry point (hooks → ingest, everything
  else → frontend, TLS terminated there), but it needs an ingress controller in the cluster
  (ingress-nginx assumed), your hostname in place of `kuruops.example.com`, and a TLS Secret named
  `kuruops-tls` -- issued by cert-manager (an example issuer is in
  [`optional/cert-manager-issuer.yaml`](optional/cert-manager-issuer.yaml)) or created by you. Set
  `TRUSTED_PROXY_CIDR` on the frontend (`07-frontend.yaml`) to the controller's pod range so the login
  rate limit sees real client addresses.
- **Autoscaling is metrics-dependent.** `11-availability.yaml` adds HorizontalPodAutoscalers for
  `api` (2–6), `ingest` (2–6) and `frontend` (2–4) and PodDisruptionBudgets for every Deployment; the
  HPAs need metrics-server in the cluster, and without one they do nothing and the replica counts
  in each manifest stand. `api` can scale because attachments live in Postgres
  (`UPLOAD_BACKEND=database` in `02-configmap.yaml`), not on a volume; if you instead keep a
  ReadWriteOnce volume with `UPLOAD_BACKEND=local`, the api is back to one replica. `analyzer` runs two
  replicas that share the analysis queue (size them against the LLM provider's rate limit, not CPU);
  `worker` runs two as a hot standby (a Postgres advisory lock per sweep tick lets one run it and the
  other skip), not for throughput.
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
kubectl apply -f deploy/k8s/06a-analyzer.yaml
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

# Recommended: deny all in-namespace ingress by default and re-open only the
# paths the stack uses (frontend and webhook ingest from outside, the API only
# from the frontend, metrics only from Prometheus). Needs a CNI that enforces
# NetworkPolicy (Calico, Cilium, ...) -- on one that does not it is silently
# inert, so verify with the throwaway-pod check in 10-networkpolicy.yaml's header.
kubectl apply -f deploy/k8s/10-networkpolicy.yaml

# PodDisruptionBudgets (node drains never take the last ingest/frontend pod) and
# HorizontalPodAutoscalers for ingest and frontend. HPAs need metrics-server.
kubectl apply -f deploy/k8s/11-availability.yaml

# The public entry point. Needs an ingress controller, your hostname, and the kuruops-tls
# Secret (see "What's NOT included" above and the header of 12-ingress.yaml).
kubectl apply -f deploy/k8s/12-ingress.yaml
```

The frontend's nginx finds the api through `API_UPSTREAM` (set in
`07-frontend.yaml` to the Service's FQDN) and resolves it on every request, so a
recreated api pod never leaves the frontend dialling a dead address. Change
`cluster.local` in that value if your cluster uses another domain. All four
backend images print and export the build they were compiled from
(`kuruops_build_info`); `KuruOpsBuildSkew` in `09-monitoring.yaml` fires when more
than one build is live for 20 minutes, i.e. a partial deploy.

Re-running the whole set after a new release is safe: the migration Job is
idempotent (golang-migrate only applies versions it hasn't recorded yet, see
`03-migration-job.yaml`'s comment), and every Deployment/Service/ConfigMap
apply is a plain declarative update. Bump the `image:` tag in
`04-api.yaml`/`05-ingest.yaml`/`06-worker.yaml`/`06a-analyzer.yaml`/`07-frontend.yaml` to
whatever your build pushed (`:latest` here is a placeholder for trying the
manifests out, not a real release-pinning strategy).

## Testing these manifests

`bash scripts/k8s-e2e.sh` deploys everything here to a throwaway kind cluster (Calico, so
NetworkPolicy is really enforced) and checks it: every Deployment becomes ready, the repository's
smoke test passes through the frontend, a webhook reaches the analyzer on another pod and gets
analysed, an attachment uploaded through one api replica is served by the other, the event stream
reaches a client through the Ingress unbuffered, a stray pod cannot reach the api, and the HPA
reads real metrics. It needs docker, kind, kubectl and openssl, and takes about ten minutes. The
alert rules in `09-monitoring.yaml` are linted and unit-tested by
`bash scripts/check-prometheus-rules.sh`.

## Images

Built the same way `docker-compose.yml` builds them locally
(`backend/Dockerfile`'s `api`/`ingest`/`analyzer`/`worker` targets,
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
