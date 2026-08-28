# Managed Postgres for the app -- same role as AWS's rds.tf. Private IP
# only (no public IP), reachable from GKE over the VPC peering set up in
# network.tf. db/migrations/ and the kuruops_app/kuruops_worker roles
# (db/init/*.sql) still need to be applied against it after this resource
# exists -- see the top-level deploy/README.md.

resource "random_password" "db_master" {
  count   = var.db_master_password == "" ? 1 : 0
  length  = 24
  special = false
}

locals {
  db_master_password = var.db_master_password != "" ? var.db_master_password : random_password.db_master[0].result
}

resource "google_sql_database_instance" "main" {
  name             = "${var.name}-${var.environment}"
  database_version = var.db_version
  region           = var.region

  settings {
    tier = var.db_tier

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.main.id
    }

    backup_configuration {
      enabled    = true
      start_time = "03:00"
    }
  }

  # Sample-appropriate, not production-appropriate -- no read replica, no
  # HA config, and deletion_protection off so `terraform destroy` actually
  # tears this down. Flip for anything real.
  deletion_protection = false

  depends_on = [
    google_project_service.required,
    google_service_networking_connection.private_services,
  ]
}

resource "google_sql_database" "kuruops" {
  name     = "kuruops"
  instance = google_sql_database_instance.main.name
}

resource "google_sql_user" "postgres" {
  name     = "postgres"
  instance = google_sql_database_instance.main.name
  password = local.db_master_password
}
