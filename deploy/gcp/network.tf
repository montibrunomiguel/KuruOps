# VPC-native (alias IP) network: one subnet with secondary ranges for Pods
# and Services, plus a private services connection so Cloud SQL gets a
# private IP reachable from GKE without a Cloud SQL Auth Proxy sidecar.

resource "google_compute_network" "main" {
  name                    = "${var.name}-${var.environment}"
  auto_create_subnetworks = false
  depends_on              = [google_project_service.required]
}

resource "google_compute_subnetwork" "main" {
  name          = "${var.name}-${var.environment}"
  network       = google_compute_network.main.id
  region        = var.region
  ip_cidr_range = var.subnet_cidr

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = var.pods_cidr
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = var.services_cidr
  }

  private_ip_google_access = true
}

resource "google_compute_router" "main" {
  name    = "${var.name}-${var.environment}"
  network = google_compute_network.main.id
  region  = var.region
}

# Nodes have no external IP (see gke.tf's private cluster config) -- Cloud
# NAT is what lets them still reach the internet (pull images from
# Artifact Registry over its public endpoint, hit external APIs from
# ingest, etc.).
resource "google_compute_router_nat" "main" {
  name                               = "${var.name}-${var.environment}"
  router                             = google_compute_router.main.name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"
}

# Reserved range + peering connection Cloud SQL's private IP is allocated
# from -- see cloudsql.tf's private_network.
resource "google_compute_global_address" "private_services" {
  name          = "${var.name}-${var.environment}-private-services"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 20
  network       = google_compute_network.main.id
}

resource "google_service_networking_connection" "private_services" {
  network                 = google_compute_network.main.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_services.name]
  depends_on              = [google_project_service.required]
}
