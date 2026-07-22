# ── P15 test log-platform: Fargate → Kinesis Firehose → S3 → Athena ─────────
# ADR 0021 (revised 2026-07-22). Off by default (var.enable_log_platform_test).
#
# This is a TEST HARNESS only — in production, P15's connector reads whatever
# log platform is already the customer's source of truth (Splunk first,
# Elasticsearch/OpenSearch second — see ADR 0021/ROADMAP P15). S3 + Athena is
# not a customer-facing connector target; it's the cheapest way to stand up a
# realistic, queryable log source to validate the LogSource interface against,
# without a continuously-billed search-engine instance.

locals {
  # Single source of truth for every cluster onboarded to this log pipeline.
  # The cluster this root module manages is always included; var.clusters
  # (default {}) is purely for additional clusters onboarded later — add one
  # entry there, not new HCL, per ADR 0021.
  log_platform_clusters = merge(
    {
      agentify_dev = {
        cluster_name = module.eks.cluster_name
        # EKS Fargate profiles reject public subnets outright (confirmed via a
        # real apply attempt, 2026-07-22 — "not a private subnet" API error).
        # Single-AZ private subnet, matched by the VPC interface endpoints
        # below (ecr.api/ecr.dkr/firehose) that give this one AZ's private
        # subnet the internet-equivalent access Fargate pods need without a
        # NAT gateway.
        subnet_ids = [module.vpc.private_subnets[0]]
        namespace  = "payments"
      }
    },
    var.clusters
  )

  active_clusters = var.enable_log_platform_test ? local.log_platform_clusters : {}
  log_bucket_name = "${local.name}-log-test"
  glue_db_name    = replace("${local.name}_logs", "-", "_")
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

# ── VPC interface endpoints — Fargate's only route to ECR/Firehose ──────────
# Private subnets have no NAT gateway (deliberate, to avoid its ~$35/mo cost)
# and Fargate profiles reject public subnets outright, so pods there have no
# other way to pull images or reach Firehose's API. Single-AZ (matches the
# single private subnet the Fargate profile above uses) to minimize the
# per-AZ interface-endpoint cost; gated behind the same toggle as everything
# else so this is $0 when not testing. S3 needs no interface endpoint — the
# existing free Gateway endpoint (main.tf) already covers ECR's S3-backed
# image-layer storage.

resource "aws_security_group" "log_test_endpoints" {
  count       = var.enable_log_platform_test ? 1 : 0
  name        = "${local.name}-log-test-endpoints"
  description = "Allow Fargate pods to reach the ECR/Firehose VPC interface endpoints"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "HTTPS from Fargate pods (cluster security group)"
    from_port       = 443
    to_port         = 443
    protocol        = "tcp"
    security_groups = [module.eks.cluster_security_group_id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_vpc_endpoint" "log_test" {
  for_each = var.enable_log_platform_test ? toset(["ecr.api", "ecr.dkr", "kinesis-firehose"]) : toset([])

  vpc_id              = module.vpc.vpc_id
  service_name        = "com.amazonaws.${var.aws_region}.${each.value}"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = [module.vpc.private_subnets[0]]
  security_group_ids  = [aws_security_group.log_test_endpoints[0].id]
  private_dns_enabled = true
}

# ── S3 (partitioned by hour) — the Firehose destination for this test harness ─

resource "aws_s3_bucket" "logs" {
  count  = var.enable_log_platform_test ? 1 : 0
  bucket = local.log_bucket_name
}

# "logs/" — primary delivered records (Hive-style year=/month=/day=/hour=
# partitioning, matched by the Glue table's partition projection below).
# "errors/" — records Firehose couldn't deliver. "athena-results/" — query
# output location. All test-harness data, not meant to be retained long.
resource "aws_s3_bucket_lifecycle_configuration" "logs" {
  count  = var.enable_log_platform_test ? 1 : 0
  bucket = aws_s3_bucket.logs[0].id

  rule {
    id     = "expire-log-test-data"
    status = "Enabled"
    filter {}
    expiration {
      days = 14
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

  # S3 only — unlike an OpenSearch destination, Firehose writing to S3 needs
  # no VPC delivery ENIs, so no ec2:CreateNetworkInterface permissions here.
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:PutObject", "s3:GetBucketLocation", "s3:ListBucket"]
      Resource = [aws_s3_bucket.logs[0].arn, "${aws_s3_bucket.logs[0].arn}/*"]
    }]
  })
}

resource "aws_kinesis_firehose_delivery_stream" "logs" {
  count       = var.enable_log_platform_test ? 1 : 0
  name        = "${local.name}-log-test"
  destination = "extended_s3"

  extended_s3_configuration {
    role_arn            = aws_iam_role.firehose_delivery[0].arn
    bucket_arn          = aws_s3_bucket.logs[0].arn
    prefix              = "logs/year=!{timestamp:yyyy}/month=!{timestamp:MM}/day=!{timestamp:dd}/hour=!{timestamp:HH}/"
    error_output_prefix = "errors/!{firehose:error-output-type}/year=!{timestamp:yyyy}/month=!{timestamp:MM}/day=!{timestamp:dd}/"
    buffering_size      = 5   # MB — small on purpose, this is low test volume
    buffering_interval  = 300 # seconds
    compression_format  = "GZIP"
  }
}

# ── Glue Data Catalog + Athena — the query surface for this test harness ────
# Partition projection (not MSCK REPAIR / a Glue crawler) so partitions never
# need a separate sync step — Athena computes them from the query's time
# range directly, matching the "bounded time window" query discipline P15
# is designed around.
#
# NOTE: validate the JSON SerDe column mapping and types against a real
# ingested record before relying on this — the Fargate log router's exact
# output shape hasn't been confirmed against a live pipeline yet.

resource "aws_glue_catalog_database" "logs" {
  count = var.enable_log_platform_test ? 1 : 0
  name  = local.glue_db_name
}

resource "aws_glue_catalog_table" "logs" {
  count         = var.enable_log_platform_test ? 1 : 0
  name          = "payments_logs"
  database_name = aws_glue_catalog_database.logs[0].name
  table_type    = "EXTERNAL_TABLE"

  parameters = {
    classification              = "json"
    "projection.enabled"        = "true"
    "projection.year.type"      = "integer"
    "projection.year.range"     = "2026,2100"
    "projection.month.type"     = "integer"
    "projection.month.range"    = "1,12"
    "projection.month.digits"   = "2"
    "projection.day.type"       = "integer"
    "projection.day.range"      = "1,31"
    "projection.day.digits"     = "2"
    "projection.hour.type"      = "integer"
    "projection.hour.range"     = "0,23"
    "projection.hour.digits"    = "2"
    "storage.location.template" = "s3://${aws_s3_bucket.logs[0].id}/logs/year=$${year}/month=$${month}/day=$${day}/hour=$${hour}/"
  }

  partition_keys {
    name = "year"
    type = "string"
  }
  partition_keys {
    name = "month"
    type = "string"
  }
  partition_keys {
    name = "day"
    type = "string"
  }
  partition_keys {
    name = "hour"
    type = "string"
  }

  storage_descriptor {
    location      = "s3://${aws_s3_bucket.logs[0].id}/logs/"
    input_format  = "org.apache.hadoop.mapred.TextInputFormat"
    output_format = "org.apache.hadoop.hive.ql.io.HiveIgnoreKeyTextOutputFormat"

    ser_de_info {
      serialization_library = "org.openx.data.jsonserde.JsonSerDe"
      parameters = {
        "mapping.timestamp" = "@timestamp"
      }
    }

    columns {
      name = "timestamp"
      type = "string"
    }
    columns {
      name = "kubernetes"
      type = "struct<cluster_name:string,namespace_name:string,pod_name:string,container_name:string,labels:struct<app:string>>"
    }
    columns {
      name = "log"
      type = "struct<level:string>"
    }
    columns {
      name = "message"
      type = "string"
    }
    columns {
      name = "stream"
      type = "string"
    }
  }
}

resource "aws_athena_workgroup" "logs" {
  count = var.enable_log_platform_test ? 1 : 0
  name  = "${local.name}-log-test"

  configuration {
    enforce_workgroup_configuration = true
    bytes_scanned_cutoff_per_query  = 1073741824 # 1 GB cap per query — cost safety net for test volume
    result_configuration {
      output_location = "s3://${aws_s3_bucket.logs[0].id}/athena-results/"
    }
  }
}

# ── Query access — grants the existing backend/agent IRSA roles (already
# attached to the pods that will run the P15 connector) Athena/Glue/S3 read
# access. No new IAM role or ServiceAccount — reuses the IRSA wiring already
# in place (module.backend_irsa / module.agent_irsa in iam.tf). ─────────────

resource "aws_iam_role_policy" "log_query_access" {
  for_each = var.enable_log_platform_test ? {
    backend = module.backend_irsa.iam_role_name
    agent   = module.agent_irsa.iam_role_name
  } : {}
  name = "${local.name}-log-query-${each.key}"
  role = each.value

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "athena:StartQueryExecution", "athena:GetQueryExecution",
          "athena:GetQueryResults", "athena:StopQueryExecution",
        ]
        Resource = [aws_athena_workgroup.logs[0].arn]
      },
      {
        Effect = "Allow"
        Action = ["glue:GetTable", "glue:GetDatabase", "glue:GetPartitions"]
        Resource = [
          "arn:aws:glue:${var.aws_region}:${data.aws_caller_identity.this.account_id}:catalog",
          aws_glue_catalog_database.logs[0].arn,
          "arn:aws:glue:${var.aws_region}:${data.aws_caller_identity.this.account_id}:table/${local.glue_db_name}/${aws_glue_catalog_table.logs[0].name}",
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:ListBucket"]
        Resource = [aws_s3_bucket.logs[0].arn, "${aws_s3_bucket.logs[0].arn}/*"]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:GetObject"]
        Resource = ["${aws_s3_bucket.logs[0].arn}/athena-results/*"]
      },
    ]
  })
}
