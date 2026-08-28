# Uploads bucket for the app's Settings -> Storage Integration feature
# (internal/blobstore's GCS backend) -- optional (see
# variables.tf.create_uploads_bucket), off by default is also fine: without
# it, api falls back to the local-disk uploads PVC already in
# deploy/k8s/04-api.yaml, which works at replicas: 1.
#
# The service-account key this produces is meant to be pasted into
# Settings -> Storage Integration in the running app, NOT wired into the
# api/ingest/worker Deployments as an env var -- same static-credential
# reasoning as the AWS stack's s3.tf.

resource "google_storage_bucket" "uploads" {
  count                        = var.create_uploads_bucket ? 1 : 0
  name                         = "${var.project_id}-${var.name}-${var.environment}-uploads"
  location                     = var.region
  uniform_bucket_level_access  = true
  force_destroy                = true

  depends_on = [google_project_service.required]
}

resource "google_service_account" "uploads" {
  count        = var.create_uploads_bucket ? 1 : 0
  account_id   = "${var.name}-${var.environment}-uploads"
  display_name = "KuruOps uploads (Settings -> Storage Integration)"
}

resource "google_storage_bucket_iam_member" "uploads" {
  count  = var.create_uploads_bucket ? 1 : 0
  bucket = google_storage_bucket.uploads[0].name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.uploads[0].email}"
}

resource "google_service_account_key" "uploads" {
  count              = var.create_uploads_bucket ? 1 : 0
  service_account_id = google_service_account.uploads[0].name
}
