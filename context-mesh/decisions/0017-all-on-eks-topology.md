# 0017 – All workloads on EKS (supersedes ADR 0004 ECS placement)

## Status

Accepted   ·   (date: 2026-06-06)   ·   Supersedes [ADR 0004](0004-tech-stack-polyglot-go-python-typescript.md) (ECS Fargate placement)

## Context

[ADR 0004](0004-tech-stack-polyglot-go-python-typescript.md) chose ECS Fargate for the
backend and agent, with the K8fy adapter as a separate in-cluster Deployment.
The adapter's RBAC (ClusterRole on pods/services/secrets/logs + Deployments)
*requires* it to run inside the monitored Kubernetes cluster; the backend and agent
needed to be reachable from it.

When choosing the deployment topology, the key forces are:

- **Adapter networking**: the adapter calls the backend over in-cluster DNS
  (`http://agentify-backend:8080`). If the backend is on ECS, this becomes a
  cross-environment call (ECS task IP / ALB) with VPC peering or PrivateLink,
  adding networking complexity and an extra failure domain.
- **Platform count**: ECS + EKS means maintaining two compute platforms, two sets
  of IAM/networking/logging configs, and two container registries (or a shared ECR
  with cross-account/cross-service access).
- **Operational simplicity** at current scale: one platform is simpler to debug,
  to deploy to, and to reason about.
- **Frontend**: a Vite/React SPA is a static build — it goes to S3 + CloudFront
  regardless of the compute choice, so it is not affected by this decision.

## Decision

**All backend, agent, and adapter workloads run as Kubernetes Deployments on a
single EKS cluster.** The frontend remains on S3 + CloudFront.

Specifically:
- `agentify-backend` — Go service, Deployment + Service (ClusterIP), exposed via
  an ALB Ingress for the ops UI and agent API.
- `agentify-agent` — Python FastAPI service, Deployment + Service (ClusterIP).
  The backend reaches it over in-cluster DNS.
- `k8fy-adapter` — Python watcher/scraper, Deployment with a ServiceAccount bound
  to the existing ClusterRole (reads pods/services/secrets/logs/deployments, no
  write permissions). Adapter also exposes the log-server on port 8200 internally.
- `agentify-frontend` — static Vite build; `aws s3 sync` to S3, CloudFront in
  front. Not a Kubernetes workload.

AWS managed services remain external to the cluster:
- **RDS Postgres** (events + current_state, ADR 0010)
- **DynamoDB** (pod registry, with the existing table)
- **Secrets Manager** (API keys, DB credentials, adapter token)
- **ECR** (container images for all three services)
- **CloudWatch** (logs via Fluent Bit, metrics)
- **ALB** (via AWS Load Balancer Controller, Ingress)

## Consequences

- **Positive:** single platform; in-cluster DNS between all services; one set of
  IAM/networking/logging; IRSA works naturally for both backend (RDS/DynamoDB) and
  adapter (read-only cluster access is already RBAC, not IAM).
- **Positive:** adapter's assumed `http://agentify-backend:8080` in the existing K8s
  manifest works without change.
- **Negative / cost accepted:** EKS control plane costs ~$73/month regardless of
  node count; ECS Fargate has no base cost. For a single low-cost dev environment
  this is the dominant infra line item.
- **Negative:** Kubernetes adds operational surface (kubectl, kubeconfig, RBAC) that
  ECS doesn't. Accepted because the adapter *requires* cluster access anyway.
- **Revisit if:** the backend/agent need to serve multiple monitored clusters
  simultaneously (then an ECS control plane outside any cluster may be cleaner);
  or cost pressure at low utilization favours ECS's zero base cost.
