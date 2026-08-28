<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# KuruOps on GCP (GKE)

Provisions the cloud infrastructure KuruOps needs: a VPC-native network, a
private-nodes GKE cluster, a Cloud SQL Postgres 16 instance (private IP), one
Artifact Registry repo holding all four images, and — optionally — a GCS
bucket for attachment uploads. It does **not** apply `deploy/k8s/`'s
manifests, run `db/migrations/`, or set up ingress/TLS/autoscaling — same
scope cut as the AWS stack, see `deploy/k8s/README.md`'s scope note.

## Prerequisites

- Terraform >= 1.5
- `gcloud` CLI authenticated (`gcloud auth application-default login`), an
  existing GCP project with billing enabled
- `kubectl`, `docker`, and `psql` on your machine

## 1. Provision the infrastructure

```bash
cd deploy/gcp
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars — at minimum set project_id

terraform init
terraform plan
terraform apply
```

`apis.tf` enables every GCP API this stack needs on the target project, so a
brand-new project works on the first `apply` without a separate
`gcloud services enable` step.

## 2. Point kubectl at the new cluster

```bash
$(terraform output -raw configure_kubectl)
kubectl get nodes
```

## 3. Build and push the four images

```bash
$(terraform output -raw artifact_registry_login_command)

cd ../..   # repo root
REPO=$(cd deploy/gcp && terraform output -raw artifact_registry_repo)
for target in api ingest worker; do
  docker build --target "$target" -t "$REPO/$target:latest" -f backend/Dockerfile .
  docker push "$REPO/$target:latest"
done
docker build -t "$REPO/frontend:latest" ./frontend
docker push "$REPO/frontend:latest"
```

## 4. Create the database roles

Cloud SQL's private IP is only reachable from inside the VPC — easiest way
to run the two `db/init/*.sql` scripts from your laptop is a
[Cloud SQL Auth Proxy](https://cloud.google.com/sql/docs/postgres/sql-proxy)
tunnel (or run this step from a pod/Cloud Shell already inside the VPC):

```bash
cd deploy/gcp
cloud-sql-proxy "$(terraform output -raw db_connection_name)" &

DB_PASS=$(terraform output -raw db_master_password)
psql "postgres://postgres:$DB_PASS@127.0.0.1:5432/kuruops?sslmode=disable" \
  -v app_password='CHANGE-ME-APP-PASSWORD' \
  -f ../../db/init/kuruops_app_role.sql
psql "postgres://postgres:$DB_PASS@127.0.0.1:5432/kuruops?sslmode=disable" \
  -v worker_password='CHANGE-ME-WORKER-PASSWORD' \
  -f ../../db/init/kuruops_worker_role.sql
```

## 5. Fill in `deploy/k8s/`'s Secret and ConfigMap

Copy `deploy/k8s/01-secret.example.yaml`, fill in:
- `DATABASE_URL_APP` / `DATABASE_URL_WORKER` — `db_private_ip` output + the
  passwords you just set for `kuruops_app`/`kuruops_worker`
- `DATABASE_URL_MIGRATE` — `db_private_ip` output + the `postgres` superuser
  password (`db_master_password` output)
- `SECRETS_ENCRYPTION_KEY` — `openssl rand -base64 32`
- `JWT_PRIVATE_KEY`/`JWT_PUBLIC_KEY` — generate per that file's own comment

Update `deploy/k8s/02-configmap.yaml`'s `APP_BASE_URL` to wherever you'll
expose the frontend Service from (see step 7).

## 6. Update the image references and apply

Bump `image:` in `deploy/k8s/04-api.yaml`, `05-ingest.yaml`,
`06-worker.yaml`, `07-frontend.yaml`, and `03-migration-job.yaml`'s
`copy-migrations` initContainer to `$REPO/<name>:latest` from step 3, then
follow `deploy/k8s/README.md`'s apply order.

## 7. Reach the app from outside the cluster

`frontend`'s Service is `ClusterIP` on purpose — no ingress controller here.
Fastest way to try it:

```bash
kubectl -n kuruops port-forward svc/frontend 8080:8080
```

For anything real, install a GKE Ingress (GCE ingress class) or switch
`frontend`'s Service to `type: LoadBalancer` for a quick external IP.

## Tearing it down

```bash
cd deploy/gcp
terraform destroy
```

`deletion_protection = false` on both the cluster and the Cloud SQL instance
means this doesn't leave anything protected behind — don't rely on that for
anything with real data in it.

## What this deliberately doesn't do

Same scope cut as the AWS stack: no ingress, no autoscaling, no Workload
Identity wiring (nothing here needs an ambient GCP identity — the GCS
uploads integration takes a static service-account key via Settings, see
`storage.tf`'s comment), zonal (not regional) cluster, no Cloud SQL HA. A
sample to build a real deployment from, not a production reference
architecture.
