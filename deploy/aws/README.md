<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# KuruOps on AWS (EKS)

Provisions the cloud infrastructure KuruOps needs: a VPC, an EKS cluster
with one managed node group, an RDS Postgres 16 instance, four ECR repos
(api/ingest/worker/frontend), and — optionally — an S3 bucket for
attachment uploads. It does **not** apply `deploy/k8s/`'s manifests, run
`db/migrations/`, or set up ingress/TLS/autoscaling — see this file's "Next
steps" and `deploy/k8s/README.md`'s own scope note for why those stay
manual.

## Prerequisites

- Terraform >= 1.5
- AWS CLI configured with credentials that can create VPCs/EKS/RDS/ECR/IAM
- `kubectl`, `docker`, and `psql` (or `migrate`) on your machine

## 1. Provision the infrastructure

```bash
cd deploy/aws
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars — at minimum pick a region/AZs you actually have quota in

terraform init
terraform plan
terraform apply
```

This takes 10–15 minutes (EKS cluster creation is the slow part).

## 2. Point kubectl at the new cluster

```bash
$(terraform output -raw configure_kubectl)
kubectl get nodes   # should show node_desired_size Ready nodes
```

## 3. Build and push the four images

```bash
$(terraform output -raw ecr_login_command)

cd ../..   # repo root
REGISTRY=$(cd deploy/aws && terraform output -json ecr_repository_urls)
for target in api ingest worker; do
  repo=$(echo "$REGISTRY" | jq -r ".$target")
  docker build --target "$target" -t "$repo:latest" -f backend/Dockerfile .
  docker push "$repo:latest"
done
repo=$(echo "$REGISTRY" | jq -r ".frontend")
docker build -t "$repo:latest" ./frontend
docker push "$repo:latest"
```

## 4. Create the database roles

`db/init/kuruops_app_role.sql` and `kuruops_worker_role.sql` set up the
two least-privilege roles the running app actually connects as (RLS only
applies to a role without `BYPASSRLS`/table ownership — see
`db/README.md`). Run them once against the RDS instance using the master
credentials:

```bash
cd deploy/aws
DB_HOST=$(terraform output -raw db_endpoint)
DB_USER=$(terraform output -raw db_master_username)
DB_PASS=$(terraform output -raw db_master_password)

psql "postgres://$DB_USER:$DB_PASS@$DB_HOST/kuruops?sslmode=require" \
  -v app_password='CHANGE-ME-APP-PASSWORD' \
  -f ../../db/init/kuruops_app_role.sql
psql "postgres://$DB_USER:$DB_PASS@$DB_HOST/kuruops?sslmode=require" \
  -v worker_password='CHANGE-ME-WORKER-PASSWORD' \
  -f ../../db/init/kuruops_worker_role.sql
```

## 5. Fill in `deploy/k8s/`'s Secret and ConfigMap

Copy `deploy/k8s/01-secret.example.yaml`, fill in:
- `DATABASE_URL_APP` / `DATABASE_URL_WORKER` — `db_endpoint` above + the
  passwords you just set for `kuruops_app`/`kuruops_worker`
- `DATABASE_URL_MIGRATE` — `db_endpoint` above + the RDS master credentials
  (`db_master_username`/`db_master_password` outputs)
- `SECRETS_ENCRYPTION_KEY` — `openssl rand -base64 32`
- `JWT_PRIVATE_KEY`/`JWT_PUBLIC_KEY` — generate per that file's own comment

Update `deploy/k8s/02-configmap.yaml`'s `APP_BASE_URL` to wherever you'll
actually expose the frontend Service from (see step 7).

## 6. Update the image references and apply

Bump `image:` in `deploy/k8s/04-api.yaml`, `05-ingest.yaml`,
`06-worker.yaml`, `07-frontend.yaml`, and `03-migration-job.yaml`'s
`copy-migrations` initContainer to the ECR URLs from step 3, then follow
`deploy/k8s/README.md`'s apply order.

## 7. Reach the app from outside the cluster

`frontend`'s Service is `ClusterIP` on purpose — this stack doesn't install
an ingress controller or the AWS Load Balancer Controller. Fastest way to
try it:

```bash
kubectl -n kuruops port-forward svc/frontend 8080:8080
```

For anything real, install the AWS Load Balancer Controller and add an
`Ingress` (or switch `frontend`'s Service to `type: LoadBalancer` for a
quick plain ALB/NLB, no path routing needed since ingest — if you expose
it separately for inbound webhooks — has its own Service).

## Tearing it down

```bash
cd deploy/aws
terraform destroy
```

`skip_final_snapshot = true` on the RDS instance means this doesn't leave a
billed snapshot behind — don't rely on that for anything with real data in
it.

## What this deliberately doesn't do

Same scope cut as `deploy/k8s/README.md`: no ingress controller, no
autoscaling, no IRSA/OIDC wiring (nothing here needs an ambient AWS
identity — the S3 uploads integration takes a static access key via
Settings, see `s3.tf`'s comment), no Multi-AZ RDS, single NAT gateway. This
is a sample to build a real deployment from, not a production reference
architecture.
