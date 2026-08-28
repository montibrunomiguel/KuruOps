# One Docker-format repo holding all four images (Artifact Registry's own
# convention -- unlike ECR's one-repo-per-image, an AR repo is a namespace
# you push multiple image names into: .../kuruops/api, .../kuruops/ingest,
# etc.).

resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "${var.name}-${var.environment}"
  format        = "DOCKER"

  depends_on = [google_project_service.required]
}
