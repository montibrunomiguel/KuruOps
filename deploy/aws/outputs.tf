output "cluster_name" {
  value = aws_eks_cluster.main.name
}

output "configure_kubectl" {
  description = "Run this to point kubectl/deploy/k8s at the new cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${aws_eks_cluster.main.name}"
}

output "db_endpoint" {
  description = "host:port for the RDS instance -- goes into deploy/k8s/01-secret.example.yaml's DATABASE_URL_* fields."
  value       = aws_db_instance.main.endpoint
}

output "db_master_username" {
  value = var.db_master_username
}

output "db_master_password" {
  value     = local.db_master_password
  sensitive = true
}

output "ecr_repository_urls" {
  description = "docker push targets -- one per image (api/ingest/worker/frontend)."
  value       = { for k, v in aws_ecr_repository.images : k => v.repository_url }
}

output "ecr_login_command" {
  value = "aws ecr get-login-password --region ${var.region} | docker login --username AWS --password-stdin ${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"
}

output "uploads_bucket_name" {
  value = var.create_uploads_bucket ? aws_s3_bucket.uploads[0].bucket : null
}

output "uploads_access_key_id" {
  description = "Paste into Settings -> Storage Integration, not into any Deployment env var -- see s3.tf's doc comment."
  value       = var.create_uploads_bucket ? aws_iam_access_key.uploads[0].id : null
}

output "uploads_secret_access_key" {
  value     = var.create_uploads_bucket ? aws_iam_access_key.uploads[0].secret : null
  sensitive = true
}
