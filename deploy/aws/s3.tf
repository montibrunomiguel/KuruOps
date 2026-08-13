# Uploads bucket for the app's Settings -> Storage Integration feature
# (internal/blobstore's S3 backend) -- optional (see
# variables.tf.create_uploads_bucket), off by default is also fine: without
# it, api falls back to the local-disk uploads PVC already in
# deploy/k8s/04-api.yaml, which works at replicas: 1.
#
# The access key/secret this produces is meant to be pasted into
# Settings -> Storage Integration in the running app, NOT wired into the
# api/ingest/worker Deployments as an env var -- that Settings feature takes
# a static credential by design (see internal/blobstore's own doc comment:
# ArgusOps is self-hosted and may not be running inside AWS at all, so it
# never assumes an ambient IAM role/IRSA).

resource "aws_s3_bucket" "uploads" {
  count  = var.create_uploads_bucket ? 1 : 0
  bucket = "${var.project}-${var.environment}-uploads-${data.aws_caller_identity.current.account_id}"

  tags = {
    Name = "${var.project}-${var.environment}-uploads"
  }
}

resource "aws_s3_bucket_public_access_block" "uploads" {
  count                   = var.create_uploads_bucket ? 1 : 0
  bucket                  = aws_s3_bucket.uploads[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "uploads" {
  count  = var.create_uploads_bucket ? 1 : 0
  bucket = aws_s3_bucket.uploads[0].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_iam_user" "uploads" {
  count = var.create_uploads_bucket ? 1 : 0
  name  = "${var.project}-${var.environment}-uploads"

  tags = {
    Name = "${var.project}-${var.environment}-uploads"
  }
}

resource "aws_iam_user_policy" "uploads" {
  count = var.create_uploads_bucket ? 1 : 0
  name  = "${var.project}-${var.environment}-uploads"
  user  = aws_iam_user.uploads[0].name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
      Resource = "${aws_s3_bucket.uploads[0].arn}/*"
    }]
  })
}

resource "aws_iam_access_key" "uploads" {
  count = var.create_uploads_bucket ? 1 : 0
  user  = aws_iam_user.uploads[0].name
}

data "aws_caller_identity" "current" {}
