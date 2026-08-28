variable "name" {
  description = "Short name prefixed onto every resource (resource group, AKS cluster, ACR, DB server...)."
  type        = string
  default     = "kuruops"
}

variable "environment" {
  type    = string
  default = "sample"
}

variable "location" {
  type    = string
  default = "eastus"
}

variable "vnet_cidr" {
  type    = string
  default = "10.70.0.0/16"
}

variable "aks_subnet_cidr" {
  type    = string
  default = "10.70.0.0/20"
}

# Postgres Flexible Server's VNet integration requires its own delegated
# subnet (Microsoft.DBforPostgreSQL/flexibleServers) -- can't share the AKS
# node subnet.
variable "db_subnet_cidr" {
  type    = string
  default = "10.70.16.0/24"
}

variable "kubernetes_version" {
  description = "Leave null to let AKS pick its current default."
  type        = string
  default     = null
}

variable "node_vm_size" {
  type    = string
  default = "Standard_D2s_v5"
}

# api/ingest/worker/frontend at replicas:1 each plus the migration Job and
# AKS's own system pods -- 2 nodes comfortably fits deploy/k8s/'s fixed
# replica counts.
variable "node_count" {
  type    = number
  default = 2
}

variable "db_sku_name" {
  description = "Postgres Flexible Server compute tier, e.g. B_Standard_B1ms (burstable, cheapest) or GP_Standard_D2s_v3."
  type        = string
  default     = "B_Standard_B1ms"
}

variable "db_storage_mb" {
  type    = number
  default = 32768
}

variable "db_version" {
  description = "Must match what db/migrations/ was written against."
  type        = string
  default     = "16"
}

variable "db_master_password" {
  description = "Password for the Postgres Flexible Server admin login. Leave empty to have Terraform generate and store one (see random_password.db_master in postgres.tf)."
  type        = string
  default     = ""
  sensitive   = true
}
