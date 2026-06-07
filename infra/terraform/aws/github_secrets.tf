# ── GitHub Actions secrets ─────────────────────────────────────────────────
# Set automatically from Terraform outputs so no manual copy-paste is needed.
# Requires a GitHub PAT with repo + secrets scope passed via TF_VAR_github_token.

provider "github" {
  token = var.github_token
  owner = var.github_owner
}

data "aws_caller_identity" "this" {}

locals {
  ecr_registry = "${data.aws_caller_identity.this.account_id}.dkr.ecr.${var.aws_region}.amazonaws.com"
}

resource "github_actions_secret" "aws_role_arn" {
  repository      = var.github_repo
  secret_name     = "AWS_ROLE_ARN"
  plaintext_value = aws_iam_role.ci.arn
}

resource "github_actions_secret" "aws_region" {
  repository      = var.github_repo
  secret_name     = "AWS_REGION"
  plaintext_value = var.aws_region
}

resource "github_actions_secret" "ecr_registry" {
  repository      = var.github_repo
  secret_name     = "ECR_REGISTRY"
  plaintext_value = local.ecr_registry
}

resource "github_actions_secret" "eks_cluster_name" {
  repository      = var.github_repo
  secret_name     = "EKS_CLUSTER_NAME"
  plaintext_value = module.eks.cluster_name
}
