#!/usr/bin/env bash
# vault-bootstrap.sh — Idempotent Vault bootstrap for namespace-segregated PKI.
#
# Creates per-namespace PKI engines, CAs, PKI roles, KV paths, Vault policies,
# and Kubernetes auth roles.  Issues an initial TLS cert per service and stores
# it in the KV store so pods can read it via Vault Agent Injector.
#
# Safe to re-run: every step checks whether the resource exists before creating.
#
# Usage (local, with port-forward running):
#   export VAULT_ADDR=http://127.0.0.1:8200
#   export VAULT_TOKEN=root
#   bash scripts/vault-bootstrap.sh
#
# Usage (CI — called by 03-vault-bootstrap.yml):
#   Kubeconfig and port-forward are set up by the workflow before this runs.

set -euo pipefail

VAULT_ADDR="${VAULT_ADDR:-http://127.0.0.1:8200}"
VAULT_TOKEN="${VAULT_TOKEN:-root}"
export VAULT_ADDR VAULT_TOKEN

# ── Service catalogue ─────────────────────────────────────────────────────────
# Format: "k8s-namespace:service1,service2,..."
# Each service gets its own PKI role, KV path, policy, and K8s auth role.
declare -a SERVICE_CATALOGUE=(
  "payments:payment-api,payment-worker"
  "agentify:agentify-backend,agentify-agent,k8fy-adapter,agentify-frontend"
)

CERT_TTL="720h"   # 30 days per cert — rotator renews at <30 days

# ── Helpers ───────────────────────────────────────────────────────────────────
info()  { echo "  [INFO]  $*"; }
ok()    { echo "  [OK]    $*"; }
skip()  { echo "  [SKIP]  $* (already exists)"; }

vault_has_mount() {
  vault secrets list -format=json 2>/dev/null \
    | python3 -c "import sys,json; print('yes' if '$1/' in json.load(sys.stdin) else 'no')" \
    2>/dev/null || echo "no"
}

vault_has_auth() {
  vault auth list -format=json 2>/dev/null \
    | python3 -c "import sys,json; print('yes' if '$1/' in json.load(sys.stdin) else 'no')" \
    2>/dev/null || echo "no"
}

vault_has_policy() {
  vault policy list 2>/dev/null | grep -qx "$1" && echo "yes" || echo "no"
}

vault_has_kv_secret() {
  vault kv get "$1" &>/dev/null && echo "yes" || echo "no"
}

# ── Step 0: verify connectivity ───────────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════════════"
echo "  Vault Bootstrap — namespace-segregated PKI"
echo "  VAULT_ADDR: $VAULT_ADDR"
echo "════════════════════════════════════════════════════"
echo ""

vault status -format=json | python3 -c "
import sys,json
s = json.load(sys.stdin)
print(f'  Vault version : {s[\"version\"]}')
print(f'  Initialized   : {s[\"initialized\"]}')
print(f'  Sealed        : {s[\"sealed\"]}')
" || { echo "ERROR: cannot reach Vault at $VAULT_ADDR"; exit 1; }
echo ""

# ── Step 1: Kubernetes auth ───────────────────────────────────────────────────
echo "── Step 1: Kubernetes auth method ──────────────────"
if [ "$(vault_has_auth kubernetes)" = "yes" ]; then
  skip "kubernetes auth"
else
  info "Enabling kubernetes auth..."
  vault auth enable kubernetes
  K8S_HOST=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null \
    || echo "https://kubernetes.default.svc")
  vault write auth/kubernetes/config kubernetes_host="$K8S_HOST"
  ok "kubernetes auth enabled → $K8S_HOST"
fi
echo ""

# ── Step 2: KV v2 store ───────────────────────────────────────────────────────
echo "── Step 2: KV v2 secrets engine ────────────────────"
if [ "$(vault_has_mount secret)" = "yes" ]; then
  skip "secret/ KV engine"
else
  vault secrets enable -path=secret kv-v2
  ok "KV v2 enabled at secret/"
fi
echo ""

# ── Step 3: Per-namespace PKI + per-service setup ────────────────────────────
for entry in "${SERVICE_CATALOGUE[@]}"; do
  NS="${entry%%:*}"
  SERVICES_RAW="${entry##*:}"
  IFS=',' read -ra SERVICES <<< "$SERVICES_RAW"

  echo "═══════════════════════════════════════"
  echo "  Namespace: $NS"
  echo "═══════════════════════════════════════"

  PKI_MOUNT="pki-${NS}"
  KV_BASE="secret/data/${NS}"

  # ── 3a. PKI engine for namespace ──────────────────────────────────────────
  echo "── PKI engine: ${PKI_MOUNT}/ ─────────────"
  if [ "$(vault_has_mount "${PKI_MOUNT}")" = "yes" ]; then
    skip "${PKI_MOUNT}/ already mounted"
  else
    vault secrets enable -path="${PKI_MOUNT}" pki
    vault secrets tune -max-lease-ttl=87600h "${PKI_MOUNT}"

    # Generate root CA for this namespace
    vault write -format=json "${PKI_MOUNT}/root/generate/internal" \
      common_name="${NS}.svc.cluster.local Root CA" \
      ttl=87600h \
      key_bits=4096 \
      | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; print(f'  CA serial: {d[\"serial_number\"]}')"

    vault write "${PKI_MOUNT}/config/urls" \
      issuing_certificates="http://vault.vault.svc.cluster.local:8200/v1/${PKI_MOUNT}/ca" \
      crl_distribution_points="http://vault.vault.svc.cluster.local:8200/v1/${PKI_MOUNT}/crl"

    ok "${PKI_MOUNT}/ PKI engine ready with ${NS}.svc.cluster.local CA"
  fi

  # Create K8s ServiceAccount in the namespace (kubectl)
  kubectl get namespace "${NS}" &>/dev/null \
    || kubectl create namespace "${NS}"

  # ── 3b. Per-service setup ─────────────────────────────────────────────────
  for SVC in "${SERVICES[@]}"; do
    SERVICE_DNS="${SVC}.${NS}.svc.cluster.local"

    echo ""
    echo "  ── Service: ${SVC} (${NS}) ────────────"

    # ServiceAccount
    if kubectl get serviceaccount "${SVC}" -n "${NS}" &>/dev/null; then
      skip "ServiceAccount ${SVC}/${NS}"
    else
      kubectl create serviceaccount "${SVC}" -n "${NS}"
      ok "ServiceAccount ${SVC} created in ${NS}"
    fi

    # PKI role
    ROLE_CHECK=$(vault read -format=json "${PKI_MOUNT}/roles/${SVC}" 2>/dev/null \
      | python3 -c "import sys,json; print('yes')" 2>/dev/null || echo "no")
    if [ "$ROLE_CHECK" = "yes" ]; then
      skip "PKI role ${PKI_MOUNT}/roles/${SVC}"
    else
      vault write "${PKI_MOUNT}/roles/${SVC}" \
        allowed_domains="${NS}.svc.cluster.local,${SVC}.${NS}.svc.cluster.local,${SVC},localhost" \
        allow_subdomains=true \
        allow_bare_domains=true \
        allow_localhost=true \
        max_ttl="${CERT_TTL}" \
        generate_lease=true \
        key_bits=2048 \
        key_type=rsa
      ok "PKI role ${PKI_MOUNT}/roles/${SVC} → DNS: ${SERVICE_DNS}"
    fi

    # Issue initial cert and store in KV
    KV_TLS_PATH="${NS}/tls/${SVC}"
    if [ "$(vault_has_kv_secret "secret/${KV_TLS_PATH}")" = "yes" ]; then
      skip "Initial cert at secret/${KV_TLS_PATH}"
    else
      info "Issuing initial TLS cert for ${SVC}..."
      CERT_DATA=$(vault write -format=json "${PKI_MOUNT}/issue/${SVC}" \
        common_name="${SERVICE_DNS}" \
        alt_names="localhost" \
        ttl="${CERT_TTL}")

      CERT=$(echo "$CERT_DATA" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['certificate'])")
      KEY=$(echo "$CERT_DATA"  | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['private_key'])")
      CA=$(echo "$CERT_DATA"   | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['issuing_ca'])")
      SERIAL=$(echo "$CERT_DATA" | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['serial_number'])")

      vault kv put "secret/${KV_TLS_PATH}" \
        certificate="${CERT}" \
        private_key="${KEY}" \
        issuing_ca="${CA}" \
        serial_number="${SERIAL}" \
        pki_mount="${PKI_MOUNT}" \
        pki_role="${SVC}"

      ok "Initial cert stored at secret/${KV_TLS_PATH} (serial: ${SERIAL})"
    fi

    # Vault policy
    POLICY_NAME="${NS}-${SVC}-policy"
    if [ "$(vault_has_policy "${POLICY_NAME}")" = "yes" ]; then
      skip "Policy ${POLICY_NAME}"
    else
      vault policy write "${POLICY_NAME}" - <<HCL
# Auto-generated policy for ${SVC} in ${NS} namespace.
# Grants: issue TLS certs, read TLS from KV, read app secrets, renew token.

path "${PKI_MOUNT}/issue/${SVC}" {
  capabilities = ["create", "update"]
}

path "${PKI_MOUNT}/cert/ca" {
  capabilities = ["read"]
}

path "secret/data/${NS}/tls/${SVC}" {
  capabilities = ["read"]
}

path "secret/data/${NS}/config" {
  capabilities = ["read"]
}

path "secret/data/${NS}/${SVC}/*" {
  capabilities = ["read"]
}

path "auth/token/renew-self" {
  capabilities = ["update"]
}
HCL
      ok "Policy ${POLICY_NAME} created"
    fi

    # Kubernetes auth role
    K8S_ROLE_CHECK=$(vault read -format=json "auth/kubernetes/role/${NS}-${SVC}" 2>/dev/null \
      | python3 -c "import sys,json; print('yes')" 2>/dev/null || echo "no")
    if [ "$K8S_ROLE_CHECK" = "yes" ]; then
      skip "K8s auth role ${NS}-${SVC}"
    else
      vault write "auth/kubernetes/role/${NS}-${SVC}" \
        bound_service_account_names="${SVC}" \
        bound_service_account_namespaces="${NS}" \
        policies="${POLICY_NAME}" \
        ttl=1h
      ok "K8s auth role ${NS}-${SVC} → SA: ${NS}/${SVC} → policy: ${POLICY_NAME}"
    fi
  done
  echo ""
done

# ── Step 4: Cert rotator policy ───────────────────────────────────────────────
echo "── Step 4: Cert rotator policy ─────────────────────"
if [ "$(vault_has_policy "cert-rotator-policy")" = "yes" ]; then
  skip "cert-rotator-policy"
else
  # Dynamically build policy covering all PKI mounts
  ROTATOR_PATHS=""
  for entry in "${SERVICE_CATALOGUE[@]}"; do
    NS="${entry%%:*}"
    PKI_MOUNT="pki-${NS}"
    ROTATOR_PATHS="${ROTATOR_PATHS}
path \"${PKI_MOUNT}/issue/*\" { capabilities = [\"create\", \"update\"] }
path \"${PKI_MOUNT}/cert/*\"  { capabilities = [\"read\"] }
path \"${PKI_MOUNT}/revoke\"  { capabilities = [\"create\", \"update\"] }
path \"secret/data/${NS}/tls/*\" { capabilities = [\"create\", \"update\", \"read\"] }"
  done

  vault policy write cert-rotator-policy - <<HCL
# Cert rotator — can issue, revoke, and store renewed certs for all namespaces.
${ROTATOR_PATHS}

path "auth/token/renew-self" { capabilities = ["update"] }
HCL
  ok "cert-rotator-policy created"
fi

# Create cert-rotator K8s auth role (runs in payments namespace by default)
ROTATOR_ROLE_CHECK=$(vault read -format=json "auth/kubernetes/role/cert-rotator" 2>/dev/null \
  | python3 -c "import sys,json; print('yes')" 2>/dev/null || echo "no")
if [ "$ROTATOR_ROLE_CHECK" = "yes" ]; then
  skip "K8s auth role cert-rotator"
else
  # ServiceAccount name from all namespaces listed in catalogue
  ALL_NS=$(for e in "${SERVICE_CATALOGUE[@]}"; do echo "${e%%:*}"; done | paste -sd,)
  vault write auth/kubernetes/role/cert-rotator \
    bound_service_account_names=cert-rotator \
    bound_service_account_namespaces="${ALL_NS}" \
    policies=cert-rotator-policy \
    ttl=1h
  ok "K8s auth role cert-rotator → namespaces: ${ALL_NS}"
fi

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════════════"
echo "  Bootstrap complete ✅"
echo ""
echo "  PKI engines:"
for entry in "${SERVICE_CATALOGUE[@]}"; do
  NS="${entry%%:*}"
  echo "    pki-${NS}/ → ${NS}.svc.cluster.local CA"
done
echo ""
echo "  Next: apply Kubernetes manifests"
echo "    kubectl apply -f infra/kubernetes/payments-test/"
echo "════════════════════════════════════════════════════"
