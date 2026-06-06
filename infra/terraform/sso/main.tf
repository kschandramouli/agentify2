# IAM Identity Center setup — runs ONCE before the main infra module.
#
# Prerequisite (one manual step, ~30 seconds):
#   1. Open https://console.aws.amazon.com/singlesignon
#   2. Click "Enable" — choose ap-southeast-2.
#   3. Run this module.
#
# Terraform CANNOT create an IAM Identity Center instance (aws_ssoadmin_instances
# is a data source only). Everything after the manual enable is fully automated here:
#   - A developer permission set (AdministratorAccess for now, scope down later)
#   - Your user account in the built-in identity store
#   - Account assignment: your user → permission set → this AWS account
#
# Usage:
#   cd infra/terraform/sso
#   terraform init
#   terraform apply \
#     -var="email=your@email.com" \
#     -var="first_name=Your" \
#     -var="last_name=Name"
#
# After apply:
#   aws configure sso --profile agentify-dev
#   aws sso login --profile agentify-dev
#   export AWS_PROFILE=agentify-dev
#   # Now run infra/terraform/bootstrap and infra/terraform/aws normally.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
  # Intentionally local state: this module creates the auth infrastructure,
  # so it cannot depend on the remote state bucket (which doesn't exist yet).
}

variable "aws_region" {
  type    = string
  default = "ap-southeast-2"
}

variable "email" {
  type        = string
  description = "Your email address — used as the SSO username and for the welcome email."
}

variable "first_name" {
  type        = string
  description = "Your first name (shown in the AWS console)."
}

variable "last_name" {
  type        = string
  description = "Your last name."
}

variable "session_duration" {
  type        = string
  default     = "PT8H"
  description = "How long an SSO session stays valid. ISO 8601 duration. Default: 8 hours."
}

provider "aws" {
  region = var.aws_region
}

# ── Read the existing SSO instance ───────────────────────────────────────────
# IAM Identity Center must already be enabled (the one manual step).
# This data source will error with a clear message if it isn't.

data "aws_ssoadmin_instances" "this" {}

locals {
  sso_instance_arn  = tolist(data.aws_ssoadmin_instances.this.arns)[0]
  identity_store_id = tolist(data.aws_ssoadmin_instances.this.identity_store_ids)[0]
}

data "aws_caller_identity" "this" {}

# ── Permission set ────────────────────────────────────────────────────────────
# AdministratorAccess for now (you control this account).
# Scope this down to a least-privilege set once the infra is stable.

resource "aws_ssoadmin_permission_set" "admin" {
  name             = "AgentifyAdmin"
  description      = "Full admin access for agentify infrastructure management."
  instance_arn     = local.sso_instance_arn
  session_duration = var.session_duration
}

resource "aws_ssoadmin_managed_policy_attachment" "admin" {
  instance_arn       = local.sso_instance_arn
  permission_set_arn = aws_ssoadmin_permission_set.admin.arn
  managed_policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}

# ── User in the built-in identity store ──────────────────────────────────────
# AWS sends a "Set your password" email to var.email after creation.

resource "aws_identitystore_user" "this" {
  identity_store_id = local.identity_store_id

  user_name    = var.email
  display_name = "${var.first_name} ${var.last_name}"

  name {
    given_name  = var.first_name
    family_name = var.last_name
  }

  emails {
    value   = var.email
    type    = "work"
    primary = true
  }
}

# ── Account assignment ────────────────────────────────────────────────────────
# Grants your user the AgentifyAdmin permission set on this account.

resource "aws_ssoadmin_account_assignment" "this" {
  instance_arn       = local.sso_instance_arn
  permission_set_arn = aws_ssoadmin_permission_set.admin.arn

  principal_type = "USER"
  principal_id   = aws_identitystore_user.this.user_id

  target_type = "AWS_ACCOUNT"
  target_id   = data.aws_caller_identity.this.account_id
}

# ── Outputs ───────────────────────────────────────────────────────────────────

output "sso_start_url" {
  value       = "https://${local.identity_store_id}.awsapps.com/start"
  description = "Use as the SSO start URL when running: aws configure sso --profile agentify-dev"
}

output "user_id" {
  value       = aws_identitystore_user.this.user_id
  description = "Identity Store user ID."
}

output "permission_set_arn" {
  value       = aws_ssoadmin_permission_set.admin.arn
}

output "next_steps" {
  value = <<-EOT
    SSO user created. AWS will email ${var.email} to set a password.

    Once the password is set, configure the CLI profile:

      aws configure sso --profile agentify-dev
        SSO start URL   : https://${local.identity_store_id}.awsapps.com/start
        SSO region      : ${var.aws_region}
        Default region  : ${var.aws_region}
        Default output  : json

    Then log in and verify:

      aws sso login --profile agentify-dev
      aws sts get-caller-identity --profile agentify-dev

    Then run bootstrap and the main module:

      export AWS_PROFILE=agentify-dev
      cd ../bootstrap && terraform init && terraform apply
  EOT
}
