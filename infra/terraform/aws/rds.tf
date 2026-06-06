# ── RDS Postgres (single-AZ for dev; Multi-AZ for prod) ─────────────────────
# ADR 0010: single Postgres store (events + current_state). Password is
# generated here and stored in Secrets Manager (see secrets.tf).

resource "random_password" "db" {
  length  = 32
  special = false   # avoids shell-escaping headaches in connection strings
}

resource "aws_db_subnet_group" "this" {
  name       = local.name
  subnet_ids = module.vpc.private_subnets
}

resource "aws_security_group" "rds" {
  name        = "${local.name}-rds"
  description = "Allow Postgres from EKS nodes only"
  vpc_id      = module.vpc.vpc_id

  ingress {
    description     = "Postgres from EKS nodes"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.eks.node_security_group_id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_instance" "this" {
  identifier        = local.name
  engine            = "postgres"
  engine_version    = "16"
  instance_class    = var.db_instance_class
  allocated_storage = 20
  storage_type      = "gp3"
  storage_encrypted = true

  db_name  = var.db_name
  username = var.db_username
  password = random_password.db.result

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.rds.id]

  # Dev: single-AZ, no deletion protection, smaller backup window.
  multi_az               = var.env == "prod"
  deletion_protection    = var.env == "prod"
  backup_retention_period = var.env == "prod" ? 7 : 1
  skip_final_snapshot    = var.env != "prod"

  # Allow the schema-init code to reach Postgres via in-cluster migration jobs
  # (not publicly accessible — only reachable from within the VPC).
  publicly_accessible = false
}
