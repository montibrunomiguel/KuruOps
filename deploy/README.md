<p align="right"><a href="README.pt-BR.md">🇧🇷 Português</a> · <b>🇺🇸 English</b></p>

# Deploying KuruOps

Two layers, kept separate on purpose:

- **`k8s/`** — the actual Kubernetes manifests for the app itself
  (api/ingest/worker/frontend + the one-off migration Job). Cloud-agnostic;
  works against any cluster that already has a reachable Postgres 16. See
  `k8s/README.md` for what's in it, what's deliberately not (ingress, TLS,
  autoscaling), and the apply order.
- **`aws/`, `gcp/`, `azure/`** — Terraform samples that provision the cloud
  infrastructure `k8s/`'s manifests assume: a VPC/VNet, a managed Kubernetes
  cluster, a managed Postgres instance, and a container registry. Each is
  independent (pick one cloud, not all three) and ends with the same
  handoff: fill in `k8s/01-secret.example.yaml` and `02-configmap.yaml`
  with that cloud's connection details, then apply `k8s/` per its own
  README.

## Picking a cloud

| | AWS | GCP | Azure |
|---|---|---|---|
| Cluster | EKS | GKE (private nodes) | AKS |
| Managed Postgres | RDS | Cloud SQL (private IP) | Postgres Flexible Server (VNet-integrated) |
| Registry | ECR (repo per image) | Artifact Registry (one repo) | ACR (one registry) |
| App upload storage | S3 (optional, `internal/blobstore` already supports it) | GCS (optional, same) | none — `internal/blobstore` has no Azure Blob backend yet; stays on the local PVC at `replicas: 1`, see `azure/README.md` |

Each `<cloud>/README.md` has the full step-by-step (provision → build/push
images → create the `kuruops_app`/`kuruops_worker` DB roles → fill in the
Secret/ConfigMap → apply `k8s/`).

## What none of this includes

Matching `k8s/README.md`'s own scope: no ingress controller, no TLS/cert
automation, no HorizontalPodAutoscaler, and no CI/CD wiring. This gets a
working cluster + database + registry provisioned and the app running
inside it — layering a real ingress, autoscaling, and a deploy pipeline on
top is left to whatever your organization already uses for that, same
reasoning `k8s/README.md` gives for making the same cut.

## Before you `terraform apply` any of these

Every cloud dir's Terraform state will contain plaintext secrets (a
generated DB master password, an S3/GCS access key) once applied — `.gitignore`
already excludes `*.tfstate`/`*.tfvars`, but for anything beyond a one-off
local try, configure a real remote backend (S3+DynamoDB, GCS, or Azure Blob)
in that cloud's `versions.tf` before running `terraform apply` — local state
on a laptop is fine for kicking the tires, not for anything a team needs to
keep re-applying against.
