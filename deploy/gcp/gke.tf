# Zonal GKE cluster (use a regional cluster for real HA), private nodes
# (no external IP -- egress goes through the Cloud NAT in network.tf) with
# a public control-plane endpoint so kubectl works straight from your
# laptop. No Workload Identity binding here -- nothing in deploy/k8s/ needs
# an ambient GCP identity (the app's own GCS integration takes a static
# service-account key via Settings, see storage.tf's comment). Add it
# yourself if you later add something that needs one.

resource "google_container_cluster" "main" {
  name     = "${var.name}-${var.environment}"
  location = var.zone

  # Managed separately below (google_container_node_pool.main) -- lets node
  # pool config (machine type, autoscaling) change without recreating the
  # cluster, and avoids the default pool's un-tunable settings.
  remove_default_node_pool = true
  initial_node_count       = 1

  network    = google_compute_network.main.id
  subnetwork = google_compute_subnetwork.main.id

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.0/28"
  }

  deletion_protection = false

  depends_on = [google_project_service.required]
}

resource "google_container_node_pool" "main" {
  name       = "${var.name}-${var.environment}-default"
  cluster    = google_container_cluster.main.name
  location   = var.zone
  node_count = var.node_count

  node_config {
    machine_type = var.node_machine_type
    disk_size_gb = 50

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]
  }
}
