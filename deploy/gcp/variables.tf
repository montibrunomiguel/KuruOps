variable "project_id" {
  description = "An existing GCP project ID (this Terraform doesn't create the project itself)."
  type        = string
}

variable "name" {
  description = "Short name prefixed onto every resource (cluster, DB instance, Artifact Registry repo...)."
  type        = string
  default     = "kuruops"
}

variable "environment" {
  type    = string
  default = "sample"
}

variable "region" {
  type    = string
  default = "us-central1"
}

variable "zone" {
  description = "Zone for the (zonal) GKE cluster -- use a regional cluster instead for real HA."
  type        = string
  default     = "us-central1-a"
}

variable "subnet_cidr" {
  type    = string
  default = "10.60.0.0/20"
}

variable "pods_cidr" {
  description = "Secondary range for Pod IPs (GKE VPC-native)."
  type        = string
  default     = "10.61.0.0/16"
}

variable "services_cidr" {
  description = "Secondary range for Service IPs (GKE VPC-native)."
  type        = string
  default     = "10.62.0.0/20"
}

variable "node_machine_type" {
  type    = string
  default = "e2-standard-2"
}

# api/ingest/worker/frontend at replicas:1 each plus the migration Job and
# GKE's own system pods -- 2 nodes at e2-standard-2 comfortably fits
# deploy/k8s/'s fixed replica counts.
variable "node_count" {
  type    = number
  default = 2
}

variable "db_tier" {
  description = "Cloud SQL machine tier."
  type        = string
  default     = "db-custom-1-3840"
}

variable "db_version" {
  description = "Must match what db/migrations/ was written against (Postgres 16)."
  type        = string
  default     = "POSTGRES_16"
}

variable "db_master_password" {
  description = "Password for the Cloud SQL 'postgres' superuser. Leave empty to have Terraform generate and store one (see random_password.db_master in cloudsql.tf)."
  type        = string
  default     = ""
  sensitive   = true
}

# Whether to create a GCS bucket + service account key for the app's
# Settings -> Storage Integration feature (internal/blobstore's GCS
# backend). Same reasoning as the AWS stack's create_uploads_bucket: that
# Settings panel takes a static credential pasted into the UI, not an
# ambient identity, because KuruOps is self-hosted and may not be running
# inside GCP at all. Set to false to stick with the default local-disk
# uploads PVC (fine at replicas: 1).
variable "create_uploads_bucket" {
  type    = bool
  default = true
}
