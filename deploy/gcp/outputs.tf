output "cluster_name" {
  value = google_container_cluster.main.name
}

output "configure_kubectl" {
  description = "Run this to point kubectl/deploy/k8s at the new cluster."
  value       = "gcloud container clusters get-credentials ${google_container_cluster.main.name} --zone ${var.zone} --project ${var.project_id}"
}

output "db_private_ip" {
  description = "Reachable only from inside the VPC (GKE nodes) -- goes into deploy/k8s/01-secret.example.yaml's DATABASE_URL_* fields."
  value       = google_sql_database_instance.main.private_ip_address
}

output "db_connection_name" {
  description = "For the Cloud SQL Auth Proxy, e.g. `cloud-sql-proxy $(terraform output -raw db_connection_name)`."
  value       = google_sql_database_instance.main.connection_name
}

output "db_master_username" {
  value = "postgres"
}

output "db_master_password" {
  value     = local.db_master_password
  sensitive = true
}

output "artifact_registry_repo" {
  description = "docker push target -- append /api, /ingest, /worker, /frontend."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "artifact_registry_login_command" {
  value = "gcloud auth configure-docker ${var.region}-docker.pkg.dev"
}

output "uploads_bucket_name" {
  value = var.create_uploads_bucket ? google_storage_bucket.uploads[0].name : null
}

output "uploads_service_account_key_json" {
  description = "Base64-decode and paste into Settings -> Storage Integration, not into any Deployment env var -- see storage.tf's comment."
  value       = var.create_uploads_bucket ? google_service_account_key.uploads[0].private_key : null
  sensitive   = true
}
