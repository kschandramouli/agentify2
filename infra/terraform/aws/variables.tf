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
  default = "1.30"
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
