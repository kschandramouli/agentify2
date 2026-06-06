# Deployment Guide

Architecture: all workloads (backend, agent, adapter) run on a single EKS cluster.
The frontend is a static Vite build served from S3 + CloudFront.
See [ADR 0017](../context-mesh/decisions/0017-all-on-eks-topology.md).

## Authentication model

| Who/what needs AWS access | How |
|---|---|
| **GitHub Actions** (ongoing CI/CD) | OIDC — no stored credentials (already in `iam.tf`) |
| **You, running bootstrap locally** | AWS IAM Identity Center SSO (browser login, auto-expiring session) |

There are **no long-lived IAM user keys** in this setup. If you already have
`~/.aws/credentials` with static keys, that works for bootstrap but is not
recommended — follow Step 0 below to replace it with short-lived SSO sessions.

---

## Step 0 — Set up AWS IAM Identity Center (one-time)

> Skip this if you already have a working `aws sso login` profile for your account.

IAM Identity Center gives you browser-based login with auto-expiring credentials.
No long-lived keys, no corporate IdP required. **One manual click** is unavoidable
(Terraform cannot create the IAM Identity Center instance itself); everything after
that is fully Terraformed.

### 0a — Enable IAM Identity Center (one console click)

1. Open the [IAM Identity Center console](https://console.aws.amazon.com/singlesignon)
2. Click **Enable** → confirm region `ap-southeast-2`
3. That's it — the instance is ready. Terraform reads it as a data source.

You need temporary credentials to run the SSO module for the first time. Use your
**root account** (temporarily) or any existing IAM user. After this step you'll
never need them again.

### 0b — Run the SSO Terraform module

```bash
cd infra/terraform/sso
terraform init
terraform apply \
  -var="email=your@email.com" \
  -var="first_name=Your" \
  -var="last_name=Name"
```

This creates:
- A permission set (`AgentifyAdmin` = AdministratorAccess; scope down later)
- Your user in the built-in identity store
- The account assignment binding them together

AWS will send a **"Set your password"** email to the address you provided.
Check it and set your password before continuing.

The `terraform output` block prints the exact `aws configure sso` command to run.

### 0c — Configure the AWS CLI profile

```bash
aws configure sso --profile agentify-dev
```

When prompted, use the values from the Terraform output `sso_start_url`:
- **SSO start URL**: value from `terraform output sso_start_url`
- **SSO region**: `ap-southeast-2`
- **Default region**: `ap-southeast-2`
- **Default output**: `json`

### 0d — Login and verify

```bash
aws sso login --profile agentify-dev
# Opens browser → click Allow → session valid 8h
aws sts get-caller-identity --profile agentify-dev
# Returns your account ID and the AgentifyAdmin role ARN
```

### 0e — Export the profile for all subsequent steps

```bash
export AWS_PROFILE=agentify-dev
```

Add to `~/.zshrc` (or `~/.bash_profile`) so it persists:
```bash
export AWS_PROFILE=agentify-dev
```

---

## Prerequisites

- AWS SSO profile configured (Step 0 above) — **no static keys needed**
- Terraform ≥ 1.6 (download from [releases.hashicorp.com](https://releases.hashicorp.com/terraform/))
- `kubectl` and `helm` installed locally
- Docker (for local builds; CI builds in GitHub Actions)

---

## Step 1 — Bootstrap Terraform remote state (once)

Make sure you're logged in: `aws sso login --profile agentify-dev` (if session expired).

```bash
export AWS_PROFILE=agentify-dev    # or set in your shell profile
cd infra/terraform/bootstrap
terraform init
terraform apply -var="aws_region=ap-southeast-2" -var="project=agentify"
# Note the outputs: state_bucket and lock_table
```

---

## Step 2 — Configure the backend

Edit `infra/terraform/aws/backend.tf` and replace `REPLACE_WITH_BOOTSTRAP_OUTPUT`
with the `state_bucket` value from Step 1.

```bash
cd infra/terraform/aws
terraform init \
  -backend-config="bucket=<state_bucket>" \
  -backend-config="dynamodb_table=agentify-tfstate-lock"
```

---

## Step 3 — Provision AWS infrastructure

```bash
cd infra/terraform/aws
terraform plan -var="env=dev"
terraform apply -var="env=dev"
```

This creates: EKS cluster, VPC, RDS Postgres, DynamoDB (pod registry), ECR repos,
Secrets Manager secrets, IRSA roles, and the ALB controller.

Note the outputs — you'll need them in the next steps:
- `cluster_name` → for kubeconfig and the `EKS_CLUSTER_NAME` GitHub secret
- `ci_role_arn` → for the `AWS_ROLE_ARN` GitHub secret
- `ecr_*_url` → for image pushes
- `anthropic_secret_arn` → to fill the API key

---

## Step 4 — Fill the Anthropic API key

```bash
aws secretsmanager put-secret-value \
  --secret-id agentify/dev/anthropic \
  --region ap-southeast-2 \
  --secret-string '{"api_key":"sk-ant-YOUR-KEY-HERE"}'
```

---

## Step 5 — Configure kubeconfig

```bash
aws eks update-kubeconfig --name <cluster_name from Step 3> --region ap-southeast-2
kubectl get nodes   # should list your nodes
```

---

## Step 6 — Patch manifests with real values

Replace the `REPLACE_WITH_TERRAFORM_OUTPUT_*` and `ACCOUNT_ID` placeholders in
`infra/kubernetes/*.yaml` with actual values from Terraform outputs.

```bash
# Example (replace ACCOUNT_ID and role ARNs):
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
BACKEND_ROLE=$(cd infra/terraform/aws && terraform output -raw backend_irsa_role_arn)
AGENT_ROLE=$(cd infra/terraform/aws && terraform output -raw agent_irsa_role_arn)
ADAPTER_ROLE=$(cd infra/terraform/aws && terraform output -raw adapter_irsa_role_arn)

sed -i "s|ACCOUNT_ID|${ACCOUNT_ID}|g" infra/kubernetes/*.yaml
sed -i "s|REPLACE_WITH_TERRAFORM_OUTPUT_backend_irsa_role_arn|${BACKEND_ROLE}|g" infra/kubernetes/backend.yaml
sed -i "s|REPLACE_WITH_TERRAFORM_OUTPUT_agent_irsa_role_arn|${AGENT_ROLE}|g" infra/kubernetes/agent.yaml
sed -i "s|REPLACE_WITH_TERRAFORM_OUTPUT_adapter_irsa_role_arn|${ADAPTER_ROLE}|g" infra/kubernetes/k8fy-adapter.yaml
```

---

## Step 7 — Create Kubernetes secrets from Secrets Manager

The manifests reference K8s Secrets by name. Create them once (or install
External Secrets Operator to sync from Secrets Manager automatically):

```bash
# DB credentials
DB_SECRET=$(aws secretsmanager get-secret-value \
  --secret-id agentify/dev/db --query SecretString --output text)
kubectl create secret generic agentify-db-secret -n agentify \
  --from-literal=host=$(echo $DB_SECRET | jq -r .host) \
  --from-literal=port=$(echo $DB_SECRET | jq -r .port) \
  --from-literal=dbname=$(echo $DB_SECRET | jq -r .dbname) \
  --from-literal=username=$(echo $DB_SECRET | jq -r .username) \
  --from-literal=password=$(echo $DB_SECRET | jq -r .password)

# Adapter token
ADAPTER_TOKEN=$(aws secretsmanager get-secret-value \
  --secret-id agentify/dev/adapter --query SecretString --output text | jq -r .token)
kubectl create secret generic agentify-adapter-secret -n agentify \
  --from-literal=token=$ADAPTER_TOKEN

# Anthropic API key
ANTHROPIC_KEY=$(aws secretsmanager get-secret-value \
  --secret-id agentify/dev/anthropic --query SecretString --output text | jq -r .api_key)
kubectl create secret generic agentify-anthropic-secret -n agentify \
  --from-literal=api_key=$ANTHROPIC_KEY
```

---

## Step 8 — First deploy

```bash
kubectl apply -f infra/kubernetes/backend.yaml
kubectl apply -f infra/kubernetes/agent.yaml
kubectl apply -f infra/kubernetes/k8fy-adapter.yaml
kubectl apply -f infra/kubernetes/ingress.yaml

kubectl rollout status deployment/agentify-backend -n agentify
kubectl rollout status deployment/agentify-agent   -n agentify
kubectl rollout status deployment/k8fy-adapter     -n agentify
```

Get the ALB address:
```bash
kubectl get ingress -n agentify
```

---

## Step 9 — GitHub Actions secrets (for CI/CD)

In your GitHub repo → Settings → Secrets → Actions, add:

| Secret | Value |
|---|---|
| `AWS_ROLE_ARN` | `ci_role_arn` from Terraform output |
| `AWS_REGION` | `ap-southeast-2` |
| `ECR_REGISTRY` | `ACCOUNT_ID.dkr.ecr.ap-southeast-2.amazonaws.com` |
| `EKS_CLUSTER_NAME` | `cluster_name` from Terraform output |

After that, every push to `main` (touching `src/` or manifests) triggers the deploy workflow automatically.

---

## Cost estimate (dev environment, ap-southeast-2)

| Resource | ~Monthly |
|---|---|
| EKS control plane | $73 |
| 2× t3.medium nodes (on-demand) | ~$60 |
| RDS db.t3.micro single-AZ | ~$15 |
| NAT gateway | ~$35 |
| DynamoDB (on-demand, low usage) | <$5 |
| ECR, Secrets Manager, CloudWatch | <$5 |
| **Total** | **~$190/month** |

To reduce: use Spot instances for nodes (~60% savings), shrink to 1 node during dev,
or use `db.t3.micro` reserved instance.
