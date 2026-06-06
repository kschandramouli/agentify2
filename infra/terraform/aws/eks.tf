# ── EKS cluster ──────────────────────────────────────────────────────────────
# Dev: managed node group (t3.medium × 1–3) — no Fargate to keep it simple.
# The cluster also needs:
#   - OIDC provider (for IRSA — service accounts that assume IAM roles)
#   - AWS Load Balancer Controller (installs via Helm, grants ALB ingress)
#   - EBS CSI driver (for persistent volumes, e.g. if Postgres moves in-cluster later)

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.0"

  cluster_name    = local.name
  cluster_version = var.cluster_version

  vpc_id                         = module.vpc.vpc_id
  subnet_ids                     = module.vpc.private_subnets
  cluster_endpoint_public_access = true   # dev convenience; tighten in prod with CIDR allowlist

  # Enable IRSA (IAM Roles for Service Accounts) — required for backend + adapter.
  enable_irsa = true

  eks_managed_node_groups = {
    main = {
      instance_types = [var.node_instance_type]
      min_size       = var.node_min
      max_size       = var.node_max
      desired_size   = var.node_desired

      # Use the latest EKS-optimised AMI automatically.
      use_latest_ami_release_version = true
    }
  }

  # Core add-ons
  cluster_addons = {
    coredns                = { most_recent = true }
    kube-proxy             = { most_recent = true }
    vpc-cni                = { most_recent = true }
    aws-ebs-csi-driver     = { most_recent = true }
  }
}

# ── kubectl / helm providers (configured post-cluster) ────────────────────────
data "aws_eks_cluster_auth" "this" {
  name = module.eks.cluster_name
}

provider "kubernetes" {
  host                   = module.eks.cluster_endpoint
  cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
  token                  = data.aws_eks_cluster_auth.this.token
}

provider "helm" {
  kubernetes {
    host                   = module.eks.cluster_endpoint
    cluster_ca_certificate = base64decode(module.eks.cluster_certificate_authority_data)
    token                  = data.aws_eks_cluster_auth.this.token
  }
}

# ── AWS Load Balancer Controller (Helm) ───────────────────────────────────────
# Watches Ingress objects and creates/updates ALBs.

resource "aws_iam_policy" "alb_controller" {
  name   = "${local.name}-alb-controller"
  policy = file("${path.module}/policies/alb-controller-policy.json")
}

module "alb_controller_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name                              = "${local.name}-alb-controller"
  attach_load_balancer_controller_policy = true

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["kube-system:aws-load-balancer-controller"]
    }
  }
}

resource "helm_release" "alb_controller" {
  name       = "aws-load-balancer-controller"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"
  namespace  = "kube-system"
  version    = "1.7.2"

  set {
    name  = "clusterName"
    value = module.eks.cluster_name
  }
  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = module.alb_controller_irsa.iam_role_arn
  }

  depends_on = [module.eks]
}

# ── agentify namespace ────────────────────────────────────────────────────────
resource "kubernetes_namespace" "agentify" {
  metadata {
    name = "agentify"
  }
  depends_on = [module.eks]
}
