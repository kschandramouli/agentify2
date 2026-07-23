#!/usr/bin/env bash
# mirror_base_images.sh — mirror the Docker Hub base images the
# 'payments-test' manifests need into ECR (infra/terraform/aws/logging.tf,
# aws_ecr_repository.log_test_base_images).
#
# Why this exists: the 'payments' namespace's Fargate profile (ADR 0021) has
# no NAT gateway — only VPC endpoints for ecr.api/ecr.dkr/kinesis-firehose —
# so Docker Hub pulls from those pods time out. Mirroring the handful of
# tags this namespace pins is cheaper than a NAT gateway and, since these
# tags never change, needs no ongoing sync.
#
# The alpine image also gets curl+jq baked in here (as tag `3.18-tools`),
# because the vault-cert-init initContainer's `apk add --no-cache curl jq`
# has exactly the same problem — apk's CDN isn't reachable from Fargate
# either. Baking the tools in at mirror time removes that second dependency.
#
# Run from AWS CloudShell (has Docker + unrestricted internet) — this script
# intentionally avoids depending on any tool a corporate proxy might corrupt
# mid-download (see ADR 0021 revision notes, 2026-07-22/23).
#
# Prerequisites:
#   • terraform apply -var enable_log_platform_test=true already run
#   • docker, aws CLI, authenticated (CloudShell has both out of the box)
#
# Usage:
#   scripts/mirror_base_images.sh

set -euo pipefail

TF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../infra/terraform/aws" && pwd)"

REPOS_JSON=$(terraform -chdir="$TF_DIR" output -json log_test_base_image_repos)
ALPINE_REPO=$(echo "$REPOS_JSON" | jq -r '.alpine // empty')
NGINX_REPO=$(echo "$REPOS_JSON" | jq -r '.nginx // empty')

if [ -z "$ALPINE_REPO" ] || [ -z "$NGINX_REPO" ]; then
  echo "ERROR: log_test_base_image_repos output is empty — is enable_log_platform_test=true applied?" >&2
  exit 1
fi

REGISTRY="${ALPINE_REPO%%/*}"
AWS_REGION="${AWS_REGION:-$(aws configure get region)}"

echo "Logging in to ECR (${REGISTRY})..."
aws ecr get-login-password --region "$AWS_REGION" | docker login --username AWS --password-stdin "$REGISTRY"

echo "Building alpine:3.18-tools (curl+jq baked in, for the vault-cert-init initContainer)..."
BUILD_DIR=$(mktemp -d)
cat > "$BUILD_DIR/Dockerfile" <<'EOF'
FROM alpine:3.18
RUN apk add --no-cache curl jq
EOF
docker build -t "${ALPINE_REPO}:3.18-tools" "$BUILD_DIR"
docker push "${ALPINE_REPO}:3.18-tools"
rm -rf "$BUILD_DIR"

echo "Mirroring plain base images (no build needed)..."
for spec in "alpine:3.19|${ALPINE_REPO}:3.19" "nginx:1.25-alpine|${NGINX_REPO}:1.25-alpine" "nginx:1.26-alpine|${NGINX_REPO}:1.26-alpine"; do
  SRC="${spec%%|*}"
  DST="${spec##*|}"
  echo "  ${SRC} -> ${DST}"
  docker pull "$SRC"
  docker tag "$SRC" "$DST"
  docker push "$DST"
done

echo ""
echo "Done. Mirrored images:"
echo "  ${ALPINE_REPO}:3.18-tools   (vault-cert-init, all 3 payments-test deployments)"
echo "  ${ALPINE_REPO}:3.19          (payment-worker main container)"
echo "  ${NGINX_REPO}:1.25-alpine    (payment-api / payment main container)"
echo "  ${NGINX_REPO}:1.26-alpine    (payment-test.yml Phase 2 rollout trigger)"
