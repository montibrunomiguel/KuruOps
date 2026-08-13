# One repo per image, matching backend/Dockerfile's three build targets plus
# the frontend image -- same four images docker-compose.yml builds locally
# (argusops-api, argusops-ingest, argusops-worker, argusops-frontend).

resource "aws_ecr_repository" "images" {
  for_each             = toset(["api", "ingest", "worker", "frontend"])
  name                 = "${var.project}/${each.key}"
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = {
    Name = "${var.project}-${var.environment}-${each.key}"
  }
}

# Keep only the last 10 images per repo -- untagged/older layers pile up fast
# with IMMUTABLE tags forcing a new tag on every push.
resource "aws_ecr_lifecycle_policy" "images" {
  for_each   = aws_ecr_repository.images
  repository = each.value.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "keep last 10 images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 10
      }
      action = { type = "expire" }
    }]
  })
}
