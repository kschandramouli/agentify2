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
# Safe to re-run: the ECR repos are image_tag_mutability=IMMUTABLE, so
# already-mirrored tags are skipped rather than re-pushed (which would fail).
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

tag_exists() {
  # $1 = repo name (e.g. agentify/log-test-base/alpine), $2 = tag
  aws ecr describe-images --region "$AWS_REGION" --repository-name "$1" --image-ids imageTag="$2" >/dev/null 2>&1
}

build_and_push() {
  # $1 = repo url, $2 = tag, $3 = base image, $4 = apk packages to bake in
  local repo="$1" tag="$2" base="$3" packages="$4"
  local repo_name="${repo#*/}"
  if tag_exists "$repo_name" "$tag"; then
    echo "  ${repo}:${tag} already exists, skipping build"
    return
  fi
  echo "Building ${repo}:${tag} (${base} + ${packages})..."
  local build_dir
  build_dir=$(mktemp -d)
  {
    echo "FROM ${base}"
    echo "RUN apk add --no-cache ${packages}"
  } > "$build_dir/Dockerfile"
  docker build -t "${repo}:${tag}" "$build_dir"
  docker push "${repo}:${tag}"
  rm -rf "$build_dir"
}

mirror_plain() {
  # $1 = source image, $2 = dest repo:tag
  local src="$1" dst="$2"
  local repo_name tag
  repo_name="${dst%:*}"; repo_name="${repo_name#*/}"
  tag="${dst##*:}"
  if tag_exists "$repo_name" "$tag"; then
    echo "  ${dst} already exists, skipping"
    return
  fi
  echo "  ${src} -> ${dst}"
  docker pull "$src"
  docker tag "$src" "$dst"
  docker push "$dst"
}

build_and_push "$ALPINE_REPO" "3.18-tools" "alpine:3.18" "curl jq"

echo "Mirroring plain base images (no build needed)..."
mirror_plain "alpine:3.19" "${ALPINE_REPO}:3.19"
mirror_plain "nginx:1.25-alpine" "${NGINX_REPO}:1.25-alpine"
mirror_plain "nginx:1.26-alpine" "${NGINX_REPO}:1.26-alpine"

echo ""
echo "Done. Mirrored images:"
echo "  ${ALPINE_REPO}:3.18-tools   (vault-cert-init, all 3 payments-test deployments)"
echo "  ${ALPINE_REPO}:3.19          (payment-worker main container)"
echo "  ${NGINX_REPO}:1.25-alpine    (payment-api / payment main container)"
echo "  ${NGINX_REPO}:1.26-alpine    (05-payment-test.yml Phase 2 rollout trigger)"
