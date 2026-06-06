# ── Secrets Manager ───────────────────────────────────────────────────────────
# Secrets are created here; values are written once manually (or by CI) after
# `terraform apply`. Workloads read them via IRSA + the AWS Secrets and Config
# Provider (ASCP) or via environment injection in the Deployment spec.
#
# Secrets created:
#   agentify/dev/db          — Postgres connection details (auto-populated)
#   agentify/dev/anthropic   — ANTHROPIC_API_KEY (fill manually)
#   agentify/dev/adapter     — ADAPTER_AUTH_TOKEN (fill manually or generate)

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

# ANTHROPIC_API_KEY — placeholder; fill after apply:
#   aws secretsmanager put-secret-value \
#     --secret-id agentify/dev/anthropic \
#     --secret-string '{"api_key":"sk-ant-..."}'
resource "aws_secretsmanager_secret" "anthropic" {
  name                    = "${var.project}/${var.env}/anthropic"
  recovery_window_in_days = var.env == "prod" ? 7 : 0
}

# ADAPTER_AUTH_TOKEN — auto-generated; backend and adapter both read from here.
resource "random_password" "adapter_token" {
  length  = 40
  special = false
}

resource "aws_secretsmanager_secret" "adapter" {
  name                    = "${var.project}/${var.env}/adapter"
  recovery_window_in_days = var.env == "prod" ? 7 : 0
}

resource "aws_secretsmanager_secret_version" "adapter" {
  secret_id     = aws_secretsmanager_secret.adapter.id
  secret_string = jsonencode({ token = random_password.adapter_token.result })
}
