provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = var.project
      Environment = var.env
      ManagedBy   = "terraform"
    }
  }
}

# Providers for the EKS cluster are configured after the cluster is created
# (in eks.tf) so kubectl/helm can reach it.

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  name   = "${var.project}-${var.env}"
  azs    = slice(data.aws_availability_zones.available.names, 0, 2)
}

# ── VPC ──────────────────────────────────────────────────────────────────────
# Public subnets: ALB (internet-facing). Private subnets: EKS nodes + RDS.
# Single NAT gateway for dev (cost saving; use one per AZ in prod).

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = local.name
  cidr = var.vpc_cidr

  azs             = local.azs
  private_subnets = [for i, az in local.azs : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets  = [for i, az in local.azs : cidrsubnet(var.vpc_cidr, 4, i + 4)]

  # No NAT gateway — EKS nodes run in public subnets (dev) and call AWS APIs
  # via free gateway endpoints (S3, DynamoDB) or directly. Saves $35/month.
  enable_nat_gateway      = false
  map_public_ip_on_launch = true   # required: EKS nodes in public subnets need a public IP
  enable_dns_hostnames    = true
  enable_dns_support      = true

  # Tags required by the AWS Load Balancer Controller (ALB ingress).
  public_subnet_tags = {
    "kubernetes.io/role/elb"                        = "1"
    "kubernetes.io/cluster/${local.name}"           = "shared"
  }
  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = "1"
  }
}

# ── Free gateway endpoints (S3 + DynamoDB) ───────────────────────────────────
# Gateway endpoints are free and keep S3/DynamoDB traffic inside the AWS
# network (faster, no data-transfer charges, no internet required for these
# services). Safe for both public and private route tables.

resource "aws_vpc_endpoint" "s3" {
  vpc_id            = module.vpc.vpc_id
  service_name      = "com.amazonaws.${var.aws_region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = concat(
    module.vpc.private_route_table_ids,
    module.vpc.public_route_table_ids,
  )
}

resource "aws_vpc_endpoint" "dynamodb" {
  vpc_id            = module.vpc.vpc_id
  service_name      = "com.amazonaws.${var.aws_region}.dynamodb"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = concat(
    module.vpc.private_route_table_ids,
    module.vpc.public_route_table_ids,
  )
}
