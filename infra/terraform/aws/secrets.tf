# ── Secrets Manager ───────────────────────────────────────────────────────────
# Secrets are created here; values are written once manually (or by CI) after
# `terraform apply`. Workloads read them via IRSA + the AWS Secrets and Config
# Provider (ASCP) or via environment injection in the Deployment spec.
#
# Secrets created:
#   agentify/dev/db          — Postgres connection details (auto-populated)
#   agentify/dev/anthropic   — ANTHROPIC_API_KEY (fill manually)
#   agentify/dev/langfuse    — LANGFUSE_PUBLIC_KEY + LANGFUSE_SECRET_KEY (fill manually)
#
# NOT here: COLLECTOR_TOKEN (agentify-discovery-secret) — a plain K8s Secret,
# minted via POST/PUT /admin/integrations and applied to the cluster manually
# (still a one-time step, not synced by CI; see infra/kubernetes/discovery.yaml),
# not Secrets Manager (agentify-discovery has no IRSA role; see iam.tf).

resource "aws_secretsmanager_secret" "db" {
  name                    = "${var.project}/${var.env}/db"
  recovery_window_in_days = var.env == "prod" ? 7 : 0
}

resource "aws_secretsmanager_secret_version" "db" {
  secret_id = aws_secretsmanager_secret.db.id
  secret_string = jsonencode({
    host     = aws_db_instance.this.address
    port     = tostring(aws_db_instance.this.port)
    dbname   = var.db_name
    username = var.db_username
    password = random_password.db.result
    url      = "postgres://${var.db_username}:${random_password.db.result}@${aws_db_instance.this.address}:${aws_db_instance.this.port}/${var.db_name}?sslmode=require"
  })
}

resource "aws_secretsmanager_secret" "anthropic" {
  name                    = "${var.project}/${var.env}/anthropic"
  recovery_window_in_days = var.env == "prod" ? 7 : 0
}

resource "aws_secretsmanager_secret_version" "anthropic" {
  secret_id     = aws_secretsmanager_secret.anthropic.id
  secret_string = jsonencode({ api_key = var.anthropic_api_key })
}

# Langfuse API keys — used by the agent for prompt management (k8fy/* prompts).
# Fill the secret value manually after terraform apply:
#   aws secretsmanager put-secret-value \
#     --secret-id agentify/dev/langfuse \
#     --secret-string '{"LANGFUSE_PUBLIC_KEY":"pk-lf-...","LANGFUSE_SECRET_KEY":"sk-lf-..."}'
resource "aws_secretsmanager_secret" "langfuse" {
  name                    = "${var.project}/${var.env}/langfuse"
  recovery_window_in_days = var.env == "prod" ? 7 : 0
}
