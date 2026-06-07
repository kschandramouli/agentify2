# ── IRSA roles (IAM Roles for Service Accounts) ──────────────────────────────
# Each workload gets a least-privilege IAM role bound to its K8s ServiceAccount
# via OIDC. No long-lived credentials in Pods.

# Backend: needs DynamoDB (pod registry) + Secrets Manager (DB, adapter token)
module "backend_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name = "${local.name}-backend"

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["agentify:agentify-backend"]
    }
  }

  role_policy_arns = {
    secrets  = aws_iam_policy.backend_secrets.arn
    dynamodb = aws_iam_policy.backend_dynamodb.arn
  }
}

resource "aws_iam_policy" "backend_secrets" {
  name = "${local.name}-backend-secrets"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"]
        Resource = [
          aws_secretsmanager_secret.db.arn,
          aws_secretsmanager_secret.adapter.arn,
        ]
      }
    ]
  })
}

resource "aws_iam_policy" "backend_dynamodb" {
  name = "${local.name}-backend-dynamodb"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem",
          "dynamodb:DeleteItem", "dynamodb:Query", "dynamodb:Scan",
        ]
        Resource = [
          aws_dynamodb_table.pod_registry.arn,
          "${aws_dynamodb_table.pod_registry.arn}/index/*",
        ]
      }
    ]
  })
}

# Agent: only needs Secrets Manager (ANTHROPIC_API_KEY)
module "agent_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name = "${local.name}-agent"

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["agentify:agentify-agent"]
    }
  }

  role_policy_arns = {
    secrets = aws_iam_policy.agent_secrets.arn
  }
}

resource "aws_iam_policy" "agent_secrets" {
  name = "${local.name}-agent-secrets"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"]
        Resource = [aws_secretsmanager_secret.anthropic.arn]
      }
    ]
  })
}

# Adapter: Kubernetes API access is via RBAC (not IAM); it only needs the
# adapter-token secret so the log server auth token can be loaded from SM.
module "adapter_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name = "${local.name}-adapter"

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["agentify:k8fy-adapter"]
    }
  }

  role_policy_arns = {
    secrets = aws_iam_policy.adapter_secrets.arn
  }
}

resource "aws_iam_policy" "adapter_secrets" {
  name = "${local.name}-adapter-secrets"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"]
        Resource = [aws_secretsmanager_secret.adapter.arn]
      }
    ]
  })
}

# CI role (assumed by GitHub Actions via OIDC — no long-lived keys in CI)
resource "aws_iam_openid_connect_provider" "github_actions" {
  url = "https://token.actions.githubusercontent.com"

  client_id_list = ["sts.amazonaws.com"]

  # GitHub's current OIDC thumbprint — rotate if GitHub rotates their cert.
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]
}

resource "aws_iam_role" "ci" {
  name = "${local.name}-ci"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = aws_iam_openid_connect_provider.github_actions.arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringLike = {
          # Lock to this repo. Change the org/repo name if needed.
          "token.actions.githubusercontent.com:sub" = "repo:kschandramouli/agentify:*"
        }
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy" "ci" {
  name = "${local.name}-ci"
  role = aws_iam_role.ci.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # ECR: push images
      {
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart",
          "ecr:CompleteLayerUpload", "ecr:PutImage",
        ]
        Resource = [for r in aws_ecr_repository.this : r.arn]
      },
      # IAM: read role ARNs for IRSA substitution in manifests during deploy
      {
        Effect   = "Allow"
        Action   = ["iam:GetRole"]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.this.account_id}:role/agentify-dev-*",
        ]
      },
      # Secrets Manager: read secrets to sync into K8s during deploy
      {
        Effect = "Allow"
        Action = ["secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"]
        Resource = [
          aws_secretsmanager_secret.db.arn,
          aws_secretsmanager_secret.anthropic.arn,
          aws_secretsmanager_secret.adapter.arn,
        ]
      },
      # EKS: kubectl + pause/resume (scale node group to 0 and back)
      {
        Effect = "Allow"
        Action = [
          "eks:DescribeCluster",
          "eks:UpdateNodegroupConfig",
          "eks:DescribeNodegroup",
        ]
        Resource = [
          module.eks.cluster_arn,
          "${module.eks.cluster_arn}/nodegroup/*",
        ]
      },
      # RDS: pause/resume. CreateDBSnapshot needs both the instance ARN and
      # the snapshot ARN (snapshot name is dynamic, hence the wildcard).
      {
        Effect = "Allow"
        Action = ["rds:StopDBInstance", "rds:StartDBInstance", "rds:DescribeDBInstances"]
        Resource = aws_db_instance.this.arn
      },
      {
        Effect = "Allow"
        Action = ["rds:CreateDBSnapshot", "rds:DescribeDBSnapshots"]
        Resource = [
          aws_db_instance.this.arn,
          "arn:aws:rds:${var.aws_region}:${data.aws_caller_identity.this.account_id}:snapshot:agentify-dev-pause-*",
        ]
      },
    ]
  })
}
