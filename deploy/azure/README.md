<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# ArgusOps on Azure (AKS)

Provisions the cloud infrastructure ArgusOps needs: a resource group + VNet,
an AKS cluster, a VNet-integrated Azure Database for PostgreSQL Flexible
Server (private access only), and an Azure Container Registry holding all
four images. It does **not** apply `deploy/k8s/`'s manifests, run
`db/migrations/`, or set up ingress/TLS/autoscaling — same scope cut as the
AWS/GCP stacks, see `deploy/k8s/README.md`'s scope note.

## Prerequisites

- Terraform >= 1.5
- `az` CLI authenticated (`az login`), with a subscription selected
- `kubectl`, `docker`, and `psql` on your machine

## No object-storage bucket here

Unlike the AWS/GCP stacks, this one doesn't provision a blob storage bucket:
`internal/blobstore` (the app's Settings -> Storage Integration feature)
only has S3 and GCS backends today, no Azure Blob Storage backend. Uploads
stay on the local-disk PVC already in `deploy/k8s/04-api.yaml`, which works
fine at `replicas: 1` (the default). If you need attachments to survive
across multiple `api` replicas on Azure, either switch that PVC's
StorageClass to Azure Files (`ReadWriteMany`), or point the app's Storage
Integration at an S3-compatible endpoint (e.g. a self-hosted MinIO) if you
have one.

## 1. Provision the infrastructure

```bash
cd deploy/azure
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars

terraform init
terraform plan
terraform apply
```

## 2. Point kubectl at the new cluster

```bash
$(terraform output -raw configure_kubectl)
kubectl get nodes
```

## 3. Build and push the four images

```bash
$(terraform output -raw acr_login_command)

cd ../..   # repo root
REGISTRY=$(cd deploy/azure && terraform output -raw acr_login_server)
for target in api ingest worker; do
  docker build --target "$target" -t "$REGISTRY/argusops/$target:latest" -f backend/Dockerfile .
  docker push "$REGISTRY/argusops/$target:latest"
done
docker build -t "$REGISTRY/argusops/frontend:latest" ./frontend
docker push "$REGISTRY/argusops/frontend:latest"
```

## 4. Create the database roles

The Flexible Server has no public endpoint — run the two `db/init/*.sql`
scripts from something already inside the VNet (an AKS pod, a jumpbox VM,
Azure Cloud Shell with VNet peering). E.g. from a throwaway pod:

```bash
kubectl run psql-tmp --rm -it --restart=Never --image=postgres:16-alpine -- \
  psql "postgres://argusops_admin:$(cd deploy/azure && terraform output -raw db_master_password)@$(cd deploy/azure && terraform output -raw db_host)/argusops?sslmode=require"
```

then paste the contents of `db/init/argusops_app_role.sql` and
`argusops_worker_role.sql` (substituting real passwords for the
`:app_password`/`:worker_password` psql variables, or run each script's
body directly).

## 5. Fill in `deploy/k8s/`'s Secret and ConfigMap

Copy `deploy/k8s/01-secret.example.yaml`, fill in:
- `DATABASE_URL_APP` / `DATABASE_URL_WORKER` — `db_host` output + the
  passwords you just set for `argusops_app`/`argusops_worker`
- `DATABASE_URL_MIGRATE` — `db_host` output + the `argusops_admin`
  credentials (`db_master_username`/`db_master_password` outputs)
- `SECRETS_ENCRYPTION_KEY` — `openssl rand -base64 32`
- `JWT_PRIVATE_KEY`/`JWT_PUBLIC_KEY` — generate per that file's own comment

Update `deploy/k8s/02-configmap.yaml`'s `APP_BASE_URL` to wherever you'll
expose the frontend Service from (see step 7).

## 6. Update the image references and apply

Bump `image:` in `deploy/k8s/04-api.yaml`, `05-ingest.yaml`,
`06-worker.yaml`, `07-frontend.yaml`, and `03-migration-job.yaml`'s
`copy-migrations` initContainer to `$REGISTRY/argusops/<name>:latest` from
step 3, then follow `deploy/k8s/README.md`'s apply order.

## 7. Reach the app from outside the cluster

`frontend`'s Service is `ClusterIP` on purpose — no ingress controller here.
Fastest way to try it:

```bash
kubectl -n argusops port-forward svc/frontend 8080:8080
```

For anything real, install the AKS-managed application routing add-on (or
your own ingress-nginx) or switch `frontend`'s Service to
`type: LoadBalancer` for a quick external IP.

## Tearing it down

```bash
cd deploy/azure
terraform destroy
```

## What this deliberately doesn't do

Same scope cut as the AWS/GCP stacks: no ingress, no autoscaling, no
workload-identity wiring (nothing here needs an ambient Azure identity —
ACR pull is granted directly to AKS's kubelet identity, the standard
pattern), no zone-redundant Postgres HA, single node pool. A sample to
build a real deployment from, not a production reference architecture.
