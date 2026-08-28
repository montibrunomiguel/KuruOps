variable "project" {
  description = "Short name prefixed onto every resource (VPC, cluster, DB, ECR repos...)."
  type        = string
  default     = "kuruops"
}

variable "environment" {
  description = "Free-form tag, e.g. \"sample\", \"staging\". Not used for branching logic, just tagging/naming."
  type        = string
  default     = "sample"
}

variable "region" {
  description = "AWS region for every resource in this stack."
  type        = string
  default     = "us-east-1"
}

variable "vpc_cidr" {
  type    = string
  default = "10.42.0.0/16"
}

# Two AZs is the minimum EKS accepts for its control plane's subnet
# requirement -- fine for a sample, bump to 3 for real HA.
variable "azs" {
  type    = list(string)
  default = ["us-east-1a", "us-east-1b"]
}

variable "kubernetes_version" {
  type    = string
  default = "1.30"
}

variable "node_instance_type" {
  type    = string
  default = "t3.medium"
}

# api/ingest/worker/frontend at replicas:1 each plus room for the
# migration Job and cluster add-ons (CoreDNS, kube-proxy, VPC CNI) --
# 2 nodes at t3.medium comfortably fits deploy/k8s/'s fixed replica counts.
variable "node_desired_size" {
  type    = number
  default = 2
}

variable "node_min_size" {
  type    = number
  default = 1
}

variable "node_max_size" {
  type    = number
  default = 3
}

variable "db_instance_class" {
  type    = string
  default = "db.t3.micro"
}

variable "db_allocated_storage" {
  type    = number
  default = 20
}

variable "db_engine_version" {
  description = "Postgres major version -- must match what db/migrations/ was written against."
  type        = string
  default     = "16"
}

# The RDS master user -- used ONLY to run db/migrations and to create the
# kuruops_app/kuruops_worker roles (see db/init/*.sql). Never handed to
# a running api/ingest/worker container; those get their own least-privilege
# roles' connection strings instead (see the top-level deploy/README.md).
variable "db_master_username" {
  type    = string
  default = "kuruops_admin"
}

variable "db_master_password" {
  description = "Master password for the RDS instance. Leave empty to have Terraform generate and store one (see random_password.db_master in rds.tf)."
  type        = string
  default     = ""
  sensitive   = true
}

# Whether to create an IAM user + access key + S3 bucket for the app's
# Settings -> Storage Integration feature (internal/blobstore's S3 backend).
# That feature takes a static access key/secret pasted into the UI, not an
# ambient IAM role -- see internal/blobstore's own doc comment: KuruOps is
# self-hosted and may not be running inside AWS at all, so it never assumes
# IRSA. Set to false if you'd rather stick with the default local-disk
# uploads PVC (fine at replicas: 1, see deploy/k8s/README.md).
variable "create_uploads_bucket" {
  type    = bool
  default = true
}
