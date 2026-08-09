# ArgusOps on Kubernetes

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
  `argusops_app`/`argusops_worker` roles and RLS policies must already exist
  there (see `db/init/*.sql` and `db/README.md`) before pointing
  `01-secret.example.yaml` at it.
- **Ingress/TLS.** `frontend`'s Service is `ClusterIP` — reaching it from
  outside the cluster (an ingress controller, a cloud LoadBalancer Service,
  `kubectl port-forward` for a quick look) is up to you.
- **Autoscaling.** Every Deployment is a fixed `replicas: 1`. `api` and
  `ingest` are safe to scale up as-is (SSE fan-out, the rate limiter, and
  the worker's sweep jobs are already Postgres-backed for exactly this —
  see the backend engineering write-up). `api` specifically also needs
  either a ReadWriteMany `uploads` PVC or the S3 storage backend (Settings
  -> Storage) once it's more than one replica, so every replica sees the
  same attachments.
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
kubectl wait --for=condition=complete --timeout=120s -n argusops job/argusops-migrate

kubectl apply -f deploy/k8s/04-api.yaml
kubectl apply -f deploy/k8s/05-ingest.yaml
kubectl apply -f deploy/k8s/06-worker.yaml
kubectl apply -f deploy/k8s/07-frontend.yaml
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
(`argusops-api:latest`, etc.) as a placeholder.

## Health checks and resource limits

`api`/`ingest`/`worker` all expose a real `/healthz` (pings the database,
503 on failure — see `internal/httpserver.HealthCheck`) and `/metrics`
(Prometheus text format, see `internal/httpserver/metrics.go`) used here as
readiness/liveness probes. `resources.requests`/`limits` mirror
`docker-compose.yml`'s `mem_limit`/`cpus` for the same services — same
caveat applies: sized for trying this out, not a production sizing
recommendation. Watch the real `argusops_db_pool_*` /
`argusops_http_request_duration_seconds` series on `/metrics` once deployed
and adjust from there.
