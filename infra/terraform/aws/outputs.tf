output "cluster_name" {
  value       = module.eks.cluster_name
  description = "EKS cluster name — use with: aws eks update-kubeconfig --name <value>"
}

output "cluster_endpoint" {
  value     = module.eks.cluster_endpoint
  sensitive = true
}

output "ecr_backend_url" {
  value = aws_ecr_repository.this["backend"].repository_url
}

output "ecr_agent_url" {
  value = aws_ecr_repository.this["agent"].repository_url
}

output "ecr_adapter_url" {
  value = aws_ecr_repository.this["k8fy-adapter"].repository_url
}

output "rds_endpoint" {
  value     = aws_db_instance.this.address
  sensitive = true
}

output "db_secret_arn" {
  value = aws_secretsmanager_secret.db.arn
}

output "anthropic_secret_arn" {
  value       = aws_secretsmanager_secret.anthropic.arn
  description = "Fill this secret with your ANTHROPIC_API_KEY after apply."
}

output "ci_role_arn" {
  value       = aws_iam_role.ci.arn
  description = "Set as AWS_ROLE_ARN in GitHub Actions secrets."
}

output "backend_irsa_role_arn" {
  value = module.backend_irsa.iam_role_arn
}

output "agent_irsa_role_arn" {
  value = module.agent_irsa.iam_role_arn
}

output "adapter_irsa_role_arn" {
  value = module.adapter_irsa.iam_role_arn
}

# ── P15 test log-platform (ADR 0021) ─────────────────────────────────────────
output "clusters" {
  value       = local.log_platform_clusters
  description = "Clusters onboarded to the log pipeline — read by scripts/onboard_cluster_logging.sh (terraform output -json clusters) so cluster config lives in exactly one place."
}

output "opensearch_endpoint" {
  value       = try(aws_opensearch_domain.logs[0].endpoint, null)
  description = "OpenSearch domain endpoint (null when enable_log_platform_test = false)."
}

output "log_platform_firehose_stream_name" {
  value       = try(aws_kinesis_firehose_delivery_stream.logs[0].name, null)
  description = "Firehose delivery stream name — used by scripts/onboard_cluster_logging.sh to render the Fargate logging ConfigMap."
}
