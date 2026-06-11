# ── ECR repositories (one per service image) ─────────────────────────────────
# Images are pushed here by CI; EKS nodes pull from ECR via the attached IAM
# policy (AmazonEC2ContainerRegistryReadOnly is attached to the node role by
# the EKS module automatically).

locals {
  ecr_repos = ["backend", "agent", "k8fy-adapter", "frontend"]
}

resource "aws_ecr_repository" "this" {
  for_each = toset(local.ecr_repos)

  name                 = "${var.project}/${each.key}"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  # Prevent accidental destroy — images survive pause/teardown cycles.
  lifecycle {
    prevent_destroy = true
  }
}

# Lifecycle policy: keep only the 10 most recent images per repo to cap storage cost.
resource "aws_ecr_lifecycle_policy" "this" {
  for_each   = aws_ecr_repository.this
  repository = each.value.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep last 10 images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 10
      }
      action = { type = "expire" }
    }]
  })
}
