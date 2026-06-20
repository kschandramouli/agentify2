# ── HashiCorp Vault — AWS resources ───────────────────────────────────────────
#
# Stores Vault credentials in AWS Secrets Manager so the bootstrap pipeline
# can retrieve them without kubectl (for non-EKS runners).
#
# The vault-unseal-keys Kubernetes secret (written by the deploy workflow)
# is the authoritative source; this Terraform resource mirrors those
# credentials into Secrets Manager for secure cross-system access.
#
# Note: Vault itself is deployed via Helm (see vault-values.yaml), not Terraform.

# ── Vault credentials secret ──────────────────────────────────────────────────
resource "aws_secretsmanager_secret" "vault_credentials" {
  name                    = "${var.project}/${var.env}/vault"
  description             = "HashiCorp Vault dev credentials (unseal key + root token)"
  recovery_window_in_days = 0   # immediate deletion for dev

  tags = {
    Project     = var.project
    Environment = var.env
    ManagedBy   = "terraform"
    Component   = "vault"
  }
}

# Placeholder version — the deploy workflow overwrites this with real values
# after Vault is initialised. Never commit real credentials here.
resource "aws_secretsmanager_secret_version" "vault_credentials" {
  secret_id = aws_secretsmanager_secret.vault_credentials.id
  secret_string = jsonencode({
    root_token = "root"                  # dev mode — overwritten by deploy workflow
    unseal_key = "dev-mode-no-unseal"    # dev mode — no unseal needed
    vault_addr = "http://vault.vault.svc.cluster.local:8200"
  })

  # Lifecycle: allow the deploy workflow to update the value without Terraform
  # treating it as drift and reverting to the placeholder.
  lifecycle {
    ignore_changes = [secret_string]
  }
}

# ── IAM policy: CI role can read Vault credentials ───────────────────────────
data "aws_iam_policy_document" "vault_secrets_read" {
  statement {
    sid    = "ReadVaultCredentials"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
    ]
    resources = [aws_secretsmanager_secret.vault_credentials.arn]
  }

  statement {
    sid    = "UpdateVaultCredentials"
    effect = "Allow"
    actions = [
      "secretsmanager:PutSecretValue",
      "secretsmanager:UpdateSecret",
    ]
    resources = [aws_secretsmanager_secret.vault_credentials.arn]
  }
}

resource "aws_iam_policy" "vault_secrets_read" {
  name        = "${var.project}-${var.env}-vault-secrets"
  description = "Allow CI roles to read/write Vault credentials from Secrets Manager"
  policy      = data.aws_iam_policy_document.vault_secrets_read.json
}

# Attach to CI role so the bootstrap pipeline can fetch the Vault token
resource "aws_iam_role_policy_attachment" "ci_vault_secrets" {
  count      = var.ci_role_name != "" ? 1 : 0
  role       = var.ci_role_name
  policy_arn = aws_iam_policy.vault_secrets_read.arn
}

# ── Vault PKI CA outputs ──────────────────────────────────────────────────────
# These are populated by the bootstrap script after PKI engines are created.
# Stored here so other Terraform modules can reference the CA ARNs.
resource "aws_secretsmanager_secret" "vault_pki_payments" {
  name                    = "${var.project}/${var.env}/vault/pki/payments"
  description             = "Vault PKI CA cert for payments namespace"
  recovery_window_in_days = 0

  tags = {
    Project     = var.project
    Environment = var.env
    Component   = "vault-pki"
    Namespace   = "payments"
  }
}

resource "aws_secretsmanager_secret_version" "vault_pki_payments" {
  secret_id     = aws_secretsmanager_secret.vault_pki_payments.id
  secret_string = jsonencode({ ca_cert = "placeholder — populated by vault-bootstrap.sh" })
  lifecycle {
    ignore_changes = [secret_string]
  }
}

resource "aws_secretsmanager_secret" "vault_pki_agentify" {
  name                    = "${var.project}/${var.env}/vault/pki/agentify"
  description             = "Vault PKI CA cert for agentify namespace"
  recovery_window_in_days = 0

  tags = {
    Project     = var.project
    Environment = var.env
    Component   = "vault-pki"
    Namespace   = "agentify"
  }
}

resource "aws_secretsmanager_secret_version" "vault_pki_agentify" {
  secret_id     = aws_secretsmanager_secret.vault_pki_agentify.id
  secret_string = jsonencode({ ca_cert = "placeholder — populated by vault-bootstrap.sh" })
  lifecycle {
    ignore_changes = [secret_string]
  }
}
