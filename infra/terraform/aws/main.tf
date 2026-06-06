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

  enable_nat_gateway     = true
  single_nat_gateway     = true   # dev cost saving; set false for HA prod
  enable_dns_hostnames   = true
  enable_dns_support     = true

  # Tags required by the AWS Load Balancer Controller (ALB ingress).
  public_subnet_tags = {
    "kubernetes.io/role/elb" = "1"
  }
  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = "1"
  }
}
