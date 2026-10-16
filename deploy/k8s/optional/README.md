# Optional: highly available Postgres (CloudNativePG)

Everything in the parent directory scales except the database: `deploy/k8s/` points the
app at a Postgres you provide (see the main README's "What's NOT included"). If that is a
single instance, it is the one component whose failure stops the whole product. The
cleanest fix is a **managed, multi-AZ Postgres** from your cloud provider (RDS Multi-AZ,
Cloud SQL HA, Azure Flexible Server zone-redundant) -- use that if you can.

If you run your own cluster and have no managed database, the files here run Postgres as a
3-instance streaming-replication cluster under the
[CloudNativePG](https://cloudnative-pg.io) operator: one primary, two replicas, automatic
failover (usually a few seconds), and a stable `kuruops-pg-rw` Service that always points at
the current primary.

| File | What it is |
|---|---|
| `postgres-cnpg.yaml` | the `Cluster` (3 instances, anti-affinity, resource requests) |
| `postgres-roles-job.yaml` | applies `db/init/*.sql` (the `kuruops_app` / `kuruops_worker` roles and grants) |

## Setup

```bash
# 1. The operator (once per cluster).
kubectl apply --server-side -f https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.25/releases/cnpg-1.25.0.yaml
kubectl -n cnpg-system rollout status deploy/cnpg-controller-manager

# 2. Secrets. The owner secret must be of type kubernetes.io/basic-auth (what
#    CloudNativePG expects); it owns the database. The second holds the passwords of the
#    two application roles.
kubectl -n kuruops create secret generic kuruops-pg-owner --type=kubernetes.io/basic-auth \
  --from-literal=username=kuruops_owner --from-literal=password="$(openssl rand -hex 24)"
kubectl -n kuruops create secret generic kuruops-pg-roles \
  --from-literal=app_password="$(openssl rand -hex 24)" \
  --from-literal=worker_password="$(openssl rand -hex 24)"

# 3. The cluster.
kubectl apply -f deploy/k8s/optional/postgres-cnpg.yaml
kubectl -n kuruops wait --for=condition=Ready cluster/kuruops-pg --timeout=300s

# 4. Migrations (03-migration-job.yaml) with DATABASE_URL_MIGRATE pointing at kuruops-pg-rw as
#    the `postgres` superuser (its password is in the kuruops-pg-superuser secret the operator
#    creates). Not as kuruops_owner: the migrations seed rows into tables with row-level
#    security FORCED, which only a superuser (or BYPASSRLS role) may do -- as the owner they fail
#    with "new row violates row-level security policy". THEN the roles and grants:
kubectl -n kuruops create configmap kuruops-db-init --from-file=db/init/
kubectl apply -f deploy/k8s/optional/postgres-roles-job.yaml
```

Then set the `DATABASE_URL_*` entries of `kuruops-secrets` to host `kuruops-pg-rw` with the
passwords from step 2 and continue with the main README. Do not point the application at the
read-only `kuruops-pg-ro` Service: KuruOps writes on every request.

## What you still own

- **Backups.** A replica is not a backup (a bad migration or a `DELETE` replicates). The cluster
  manifest has a commented `backup.barmanObjectStore` block for continuous WAL archiving to an
  object store; enable it and test a restore. The `08-backup-cronjob.yaml` `pg_dump` in the
  parent directory is a second, independent safety net.
- **Storage class.** `storage.storageClass` is left to the cluster default; pick one with
  volume expansion and snapshots.
- **Tested.** The manifests here were applied to a kind cluster: three instances came up, the
  migrations and the roles Job ran against it, and deleting the primary pod promoted a replica
  within seconds with the data intact and writes accepted through the same `kuruops-pg-rw`.
- **Failover is not free of blips.** In-flight requests during the few seconds of a failover
  fail and the application retries on its next call -- every KuruOps process reconnects without
  a restart (verified by restarting Postgres under a running stack).
