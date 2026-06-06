# Bootstrap: creates the S3 bucket and DynamoDB table that Terraform uses for
# remote state. Run this ONCE manually before `terraform init` in infra/terraform/aws.
#
# Usage:
#   cd infra/terraform/bootstrap
#   terraform init
#   terraform apply -var="aws_region=ap-southeast-2" -var="project=agentify"
#
# After apply, copy the bucket name + table name into infra/terraform/aws/backend.tf.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
  # Bootstrap itself uses local state (it creates the remote state backend).
}

variable "aws_region" {
  type    = string
  default = "ap-southeast-2"
}

variable "project" {
  type    = string
  default = "agentify"
}

provider "aws" {
  region = var.aws_region
}

# Unique suffix so the bucket name is globally unique.
resource "random_id" "suffix" {
  byte_length = 4
}

resource "aws_s3_bucket" "tfstate" {
  bucket        = "${var.project}-tfstate-${random_id.suffix.hex}"
  force_destroy = false

  tags = {
    Project   = var.project
    ManagedBy = "terraform-bootstrap"
  }
}

resource "aws_s3_bucket_versioning" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "tfstate" {
  bucket = aws_s3_bucket.tfstate.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "tfstate" {
  bucket                  = aws_s3_bucket.tfstate.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_dynamodb_table" "tflock" {
  name         = "${var.project}-tfstate-lock"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  tags = {
    Project   = var.project
    ManagedBy = "terraform-bootstrap"
  }
}

output "state_bucket" {
  value       = aws_s3_bucket.tfstate.bucket
  description = "Set as 'bucket' in infra/terraform/aws/backend.tf"
}

output "lock_table" {
  value       = aws_dynamodb_table.tflock.name
  description = "Set as 'dynamodb_table' in infra/terraform/aws/backend.tf"
}
