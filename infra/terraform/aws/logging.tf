# ── P15 test log-platform: Fargate → Kinesis Firehose → OpenSearch ───────────
# ADR 0021. Off by default (var.enable_log_platform_test) — the OpenSearch
# domain is the dominant cost item here and should only run during an active
# test session. See ADR 0021 for the full design rationale.

locals {
  # Single source of truth for every cluster onboarded to this log pipeline.
  # The cluster this root module manages is always included; var.clusters
  # (default {}) is purely for additional clusters onboarded later — add one
  # entry there, not new HCL, per ADR 0021.
  log_platform_clusters = merge(
    {
      agentify_dev = {
        cluster_name = module.eks.cluster_name
        subnet_ids   = module.vpc.public_subnets # same subnets the EC2 node group
        # already uses — free egress via
        # IGW, no NAT/endpoint cost
        namespace = "payments"
      }
    },
    var.clusters
  )

  active_clusters = var.enable_log_platform_test ? local.log_platform_clusters : {}
}

# ── Fargate profile(s) — one per onboarded cluster, for_each over the registry ─

resource "aws_iam_role" "fargate_log_router" {
  count = var.enable_log_platform_test ? 1 : 0
  name  = "${local.name}-fargate-log-router"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "eks-fargate-pods.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "fargate_pod_execution" {
  count      = var.enable_log_platform_test ? 1 : 0
  role       = aws_iam_role.fargate_log_router[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSFargatePodExecutionRolePolicy"
}

# Required for Fargate's built-in log router to ship records — it runs under
# the pod execution role's permissions, not a separate identity.
resource "aws_iam_role_policy" "fargate_log_router_firehose" {
  count = var.enable_log_platform_test ? 1 : 0
  name  = "${local.name}-fargate-log-router-firehose"
  role  = aws_iam_role.fargate_log_router[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["firehose:PutRecordBatch"]
      Resource = [aws_kinesis_firehose_delivery_stream.logs[0].arn]
    }]
  })
}

resource "aws_eks_fargate_profile" "log_test" {
  for_each = local.active_clusters

  cluster_name           = each.value.cluster_name
  fargate_profile_name   = "log-test-${each.key}"
  pod_execution_role_arn = aws_iam_role.fargate_log_router[0].arn
  subnet_ids             = each.value.subnet_ids

  selector {
    namespace = each.value.namespace
  }
}

# ── Kinesis Firehose delivery stream → OpenSearch (+ S3 failure backup) ──────

resource "aws_s3_bucket" "firehose_backup" {
  count  = var.enable_log_platform_test ? 1 : 0
  bucket = "${local.name}-log-test-firehose-backup"
}

# Failure-backup data only — not meant to be retained. Short lifecycle.
resource "aws_s3_bucket_lifecycle_configuration" "firehose_backup" {
  count  = var.enable_log_platform_test ? 1 : 0
  bucket = aws_s3_bucket.firehose_backup[0].id

  rule {
    id     = "expire-backup-records"
    status = "Enabled"
    filter {} # applies to every object in the bucket
    expiration {
      days = 7
    }
  }
}

resource "aws_iam_role" "firehose_delivery" {
  count = var.enable_log_platform_test ? 1 : 0
  name  = "${local.name}-firehose-delivery"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "firehose.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "firehose_delivery" {
  count = var.enable_log_platform_test ? 1 : 0
  name  = "${local.name}-firehose-delivery"
  role  = aws_iam_role.firehose_delivery[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:GetBucketLocation", "s3:ListBucket"]
        Resource = [aws_s3_bucket.firehose_backup[0].arn, "${aws_s3_bucket.firehose_backup[0].arn}/*"]
      },
      {
        Effect = "Allow"
        Action = [
          "es:ESHttpPost", "es:ESHttpPut", "es:ESHttpGet",
          "es:DescribeElasticsearchDomain", "es:DescribeDomain",
        ]
        Resource = ["${aws_opensearch_domain.logs[0].arn}", "${aws_opensearch_domain.logs[0].arn}/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["ec2:CreateNetworkInterface", "ec2:DeleteNetworkInterface", "ec2:DescribeNetworkInterfaces"]
        Resource = ["*"] # required for Firehose's VPC-delivery ENIs into the OpenSearch domain's subnets
      },
    ]
  })
}

resource "aws_kinesis_firehose_delivery_stream" "logs" {
  count       = var.enable_log_platform_test ? 1 : 0
  name        = "${local.name}-log-test"
  destination = "opensearch"

  opensearch_configuration {
    domain_arn = aws_opensearch_domain.logs[0].arn
    role_arn   = aws_iam_role.firehose_delivery[0].arn
    index_name = "logs"

    vpc_config {
      subnet_ids         = [module.vpc.private_subnets[0]] # single-AZ domain — one subnet is enough
      security_group_ids = [aws_security_group.firehose_delivery[0].id]
      role_arn           = aws_iam_role.firehose_delivery[0].arn
    }

    s3_backup_mode = "FailedDocumentsOnly"

    s3_configuration {
      role_arn   = aws_iam_role.firehose_delivery[0].arn
      bucket_arn = aws_s3_bucket.firehose_backup[0].arn
    }
  }
}

# ── OpenSearch domain (VPC-based, IAM-authenticated, single instance) ───────

resource "aws_security_group" "opensearch" {
  count       = var.enable_log_platform_test ? 1 : 0
  name        = "${local.name}-opensearch"
  description = "Allow OpenSearch queries from EKS nodes + Firehose VPC delivery"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "OpenSearch from EKS nodes (backend/agent querying logs)"
    from_port       = 443
    to_port         = 443
    protocol        = "tcp"
    security_groups = [module.eks.node_security_group_id, aws_security_group.firehose_delivery[0].id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "firehose_delivery" {
  count       = var.enable_log_platform_test ? 1 : 0
  name        = "${local.name}-firehose-delivery"
  description = "Firehose's VPC-delivery ENIs writing into the OpenSearch domain"
  vpc_id      = module.vpc.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_opensearch_domain" "logs" {
  count          = var.enable_log_platform_test ? 1 : 0
  domain_name    = "${local.name}-logs"
  engine_version = "OpenSearch_2.15"

  cluster_config {
    instance_type            = var.opensearch_instance_type
    instance_count           = 1
    dedicated_master_enabled = false
    zone_awareness_enabled   = false # single-AZ — cheapest viable config for test volume
  }

  ebs_options {
    ebs_enabled = true
    volume_type = "gp3"
    volume_size = 10
  }

  vpc_options {
    subnet_ids         = [module.vpc.private_subnets[0]] # single-AZ — same private subnets RDS already uses
    security_group_ids = [aws_security_group.opensearch[0].id]
  }

  # IAM-based access control (matches P15's IRSA auth decision) — no internal
  # fine-grained-access-control user database needed for this.
  access_policies = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        AWS = [
          module.backend_irsa.iam_role_arn,
          module.agent_irsa.iam_role_arn,
          aws_iam_role.firehose_delivery[0].arn,
        ]
      }
      Action   = "es:*"
      Resource = "arn:aws:es:${var.aws_region}:${data.aws_caller_identity.this.account_id}:domain/${local.name}-logs/*"
    }]
  })
}
