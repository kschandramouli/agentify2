variable "aws_region" {
  type    = string
  default = "ap-southeast-2"
}

variable "project" {
  type    = string
  default = "agentify"
}

variable "env" {
  type    = string
  default = "dev"
  validation {
    condition     = contains(["dev", "prod"], var.env)
    error_message = "env must be dev or prod."
  }
}

# EKS
variable "cluster_version" {
  type    = string
  default = "1.33"
}

# Low-cost node group for dev; scale up in prod.
variable "node_instance_type" {
  type    = string
  default = "t3.medium"
}

variable "node_min" {
  type    = number
  default = 1
}

variable "node_max" {
  type    = number
  default = 3
}

variable "node_desired" {
  type    = number
  default = 2
}

# RDS
variable "db_instance_class" {
  type    = string
  default = "db.t3.micro"
}

variable "db_name" {
  type    = string
  default = "agentify"
}

variable "db_username" {
  type    = string
  default = "agentify"
}

# VPC CIDR — single VPC for everything.
variable "vpc_cidr" {
  type    = string
  default = "10.0.0.0/16"
}

variable "anthropic_api_key" {
  type        = string
  sensitive   = true
  description = "Anthropic API key stored in Secrets Manager. Pass via TF_VAR_anthropic_api_key env var — never hardcode."
}

variable "github_token" {
  type        = string
  sensitive   = true
  description = "GitHub PAT with repo+secrets scope. Used to set GitHub Actions secrets from Terraform outputs. Pass via TF_VAR_github_token."
}

variable "github_repo" {
  type        = string
  default     = "agentify"
  description = "GitHub repository name (without owner prefix)."
}

variable "github_owner" {
  type        = string
  default     = "kschandramouli"
  description = "GitHub account/org that owns the repository."
}

variable "ci_role_name" {
  type        = string
  default     = ""
  description = "Name of the CI IAM role to attach Vault secrets read policy to. Leave empty to skip attachment."
}
