"""Tool definitions for the K8fy agent (Claude can call these)."""

import logging
import os
from typing import Any, Dict

import httpx

logger = logging.getLogger(__name__)

# Vault connectivity (injected via env vars in the agent deployment).
# If VAULT_ADDR is not set the Vault tools return a graceful "not configured" message.
_VAULT_ADDR = os.environ.get("VAULT_ADDR", "")
_VAULT_TOKEN = os.environ.get("VAULT_TOKEN", "")

# Tool schema for Claude to understand what it can call
TOOLS = [
    {
        "name": "query_pod",
        "description": "Query details about a specific pod: phase, ready status, restart count, recent events.",
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
            },
            "required": ["pod_id", "namespace"],
        },
    },
    {
        "name": "get_service_health",
        "description": "Get health status of a service: endpoints, ready ratio, pod statuses.",
        "input_schema": {
            "type": "object",
            "properties": {
                "service_name": {"type": "string", "description": "Service name"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
            },
            "required": ["service_name", "namespace"],
        },
    },
    {
        "name": "get_certificates",
        "description": "Get certificate status: list of certs, expiry dates, renewal needs.",
        "input_schema": {
            "type": "object",
            "properties": {
                "namespace": {
                    "type": "string",
                    "description": "Kubernetes namespace (optional, all if not provided)",
                },
            },
        },
    },
    {
        "name": "get_pod_events",
        "description": "Get recent events for a pod: restarts, crashes, warnings.",
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "limit": {"type": "integer", "description": "Number of recent events to return"},
            },
            "required": ["pod_id", "namespace"],
        },
    },
    {
        "name": "get_change_history",
        "description": (
            "Get recent deployment/change events (rollouts) for a service over a "
            "time window. Use this during diagnosis to see WHAT CHANGED before a "
            "symptom began — a rollout shortly before restarts started is a likely "
            "trigger to investigate (correlation, not proof)."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "deployment": {"type": "string", "description": "Deployment/service name to filter to."},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "since": {"type": "string", "description": "RFC3339 start of the window (optional)."},
                "until": {"type": "string", "description": "RFC3339 end of the window (optional)."},
            },
            "required": ["namespace"],
        },
    },
    {
        "name": "get_pod_logs",
        "description": (
            "Fetch a bounded, redacted tail of a pod's logs to find the CRASH REASON "
            "(OOMKilled, panic/stack trace, connection refused, failing probe). Set "
            "previous=true to read the last crashed container instance — that is where "
            "a CrashLoopBackOff reason usually is. Logs are best-effort redacted and "
            "not stored; quote the relevant failure line in your answer."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "container": {"type": "string", "description": "Container name (optional; defaults to the pod's container)."},
                "previous": {"type": "boolean", "description": "Read the previous (crashed) container instance."},
                "tail_lines": {"type": "integer", "description": "Lines from the end (default 100, capped server-side)."},
            },
            "required": ["pod_id", "namespace"],
        },
    },
    {
        "name": "get_metrics_history",
        "description": (
            "Get the restart-count time-series for a pod over a time window — to "
            "see WHEN restarts started climbing (the temporal trend), not just the "
            "current count. Use this when diagnosing to find when a problem began. "
            "Samples are cumulative restart counts; a rising series means restarts "
            "happened in that window."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier to filter the series to."},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "since": {"type": "string", "description": "RFC3339 start of the window (optional)."},
                "until": {"type": "string", "description": "RFC3339 end of the window (optional)."},
                "order": {"type": "string", "enum": ["asc", "desc"], "description": "Chronological (asc) or recent-first (desc). Use asc to read a trend."},
                "limit": {"type": "integer", "description": "Max samples (default 100)."},
            },
            "required": ["pod_id", "namespace"],
        },
    },
    # ── Vault tools (requires VAULT_ADDR + VAULT_TOKEN env vars) ─────────────
    {
        "name": "get_vault_cert_status",
        "description": (
            "Check the TLS certificate managed by HashiCorp Vault PKI for a given role. "
            "Returns expiry date, days remaining, serial number, and whether rotation is recommended. "
            "Use when the user asks about Vault-managed certs, SSL expiry from Vault, or cert health."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "pki_role": {
                    "type": "string",
                    "description": "Vault PKI role name (e.g. 'payment-service').",
                },
                "kv_path": {
                    "type": "string",
                    "description": "Vault KV path where the cert is stored (e.g. 'secret/data/payments/tls'). Optional.",
                },
            },
            "required": ["pki_role"],
        },
    },
    {
        "name": "rotate_vault_cert",
        "description": (
            "Request a new TLS certificate from HashiCorp Vault PKI, store in Vault KV, "
            "and update the Kubernetes TLS Secret so it takes effect immediately. "
            "Only call this when expiry is imminent or when explicitly requested by the operator."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "pki_mount": {
                    "type": "string",
                    "description": "Vault PKI mount (e.g. 'pki-payments'). Defaults to 'pki-payments'.",
                    "default": "pki-payments",
                },
                "pki_role": {
                    "type": "string",
                    "description": "Vault PKI role name to issue from (e.g. 'payment-api').",
                },
                "common_name": {
                    "type": "string",
                    "description": "Common name for the new cert (e.g. 'payment.payments.svc.cluster.local').",
                },
                "ttl": {
                    "type": "string",
                    "description": "Desired cert TTL (e.g. '24h'). Defaults to 24h.",
                    "default": "24h",
                },
                "k8s_secret_name": {
                    "type": "string",
                    "description": "K8s TLS Secret to update with the new cert (e.g. 'payment-tls').",
                },
                "k8s_namespace": {
                    "type": "string",
                    "description": "Namespace of the K8s Secret (e.g. 'payments').",
                },
            },
            "required": ["pki_role", "common_name"],
        },
    },
]

# ── Vault tool implementations ────────────────────────────────────────────────

async def _vault_get_cert_status(pki_role: str, kv_path: str = "") -> Dict[str, Any]:
    """Read cert metadata from Vault KV and compute expiry."""
    if not _VAULT_ADDR:
        return {"error": "VAULT_ADDR not configured — Vault tools are unavailable in this environment."}

    headers = {"X-Vault-Token": _VAULT_TOKEN} if _VAULT_TOKEN else {}
    result: Dict[str, Any] = {"pki_role": pki_role, "vault_addr": _VAULT_ADDR}

    try:
        async with httpx.AsyncClient(timeout=8.0) as client:
            # Try KV path first if provided.
            if kv_path:
                resp = await client.get(f"{_VAULT_ADDR}/v1/{kv_path}", headers=headers)
                if resp.status_code == 200:
                    data = resp.json().get("data", {}).get("data", {})
                    cert_pem = data.get("certificate", "")
                    result["kv_path"] = kv_path
                    result["renewed_at"] = data.get("renewed_at")
                    result["serial"] = data.get("serial")
                    if cert_pem:
                        # Parse expiry via openssl-compatible approach using stdlib.
                        import ssl, datetime
                        try:
                            import subprocess, tempfile
                            with tempfile.NamedTemporaryFile(suffix=".pem", mode="w", delete=False) as f:
                                f.write(cert_pem)
                                tmp = f.name
                            out = subprocess.run(
                                ["openssl", "x509", "-enddate", "-noout", "-in", tmp],
                                capture_output=True, text=True,
                            ).stdout.strip()
                            date_str = out.split("=", 1)[1]
                            expiry = datetime.datetime.strptime(date_str, "%b %d %H:%M:%S %Y %Z")
                            days = (expiry - datetime.datetime.utcnow()).days
                            result["expiry"] = expiry.isoformat()
                            result["days_remaining"] = days
                            result["rotation_recommended"] = days < 30
                            result["status"] = "critical" if days < 7 else "warning" if days < 30 else "healthy"
                        except Exception:
                            result["cert_parse_error"] = "openssl not available — install in agent image"

            # Always check Vault PKI CA expiry.
            ca_resp = await client.get(f"{_VAULT_ADDR}/v1/pki/cert/ca", headers=headers)
            if ca_resp.status_code == 200:
                result["pki_ca_serial"] = ca_resp.json().get("data", {}).get("serial_number")

    except httpx.HTTPError as e:
        result["error"] = f"Vault unreachable: {e}"

    return result


async def _vault_rotate_cert(
    pki_role: str,
    common_name: str,
    ttl: str = "24h",
    pki_mount: str = "pki-payments",
    k8s_secret_name: str = "",
    k8s_namespace: str = "",
) -> Dict[str, Any]:
    """Issue a new cert from Vault PKI and update the K8s TLS Secret in-place."""
    if not _VAULT_ADDR:
        return {"error": "VAULT_ADDR not configured — Vault tools are unavailable."}

    headers = {"X-Vault-Token": _VAULT_TOKEN, "Content-Type": "application/json"} if _VAULT_TOKEN else {}
    import base64, datetime

    try:
        async with httpx.AsyncClient(timeout=15.0, verify=False) as client:
            # 1. Issue cert from Vault PKI.
            resp = await client.post(
                f"{_VAULT_ADDR}/v1/{pki_mount}/issue/{pki_role}",
                headers=headers,
                json={"common_name": common_name, "ttl": ttl},
            )
            if resp.status_code != 200:
                return {"error": f"Vault PKI issue failed ({resp.status_code}): {resp.text}"}
            data = resp.json().get("data", {})
            serial = data.get("serial_number", "")
            cert_pem = data.get("certificate", "") + "\n" + data.get("issuing_ca", "")
            key_pem  = data.get("private_key", "")

            result: Dict[str, Any] = {
                "status": "rotated",
                "pki_mount": pki_mount,
                "pki_role": pki_role,
                "common_name": common_name,
                "serial": serial,
                "ttl": ttl,
            }

            # 2. Update K8s TLS Secret via in-cluster API.
            if k8s_secret_name and k8s_namespace:
                try:
                    sa_token = open("/var/run/secrets/kubernetes.io/serviceaccount/token").read()
                    k8s_headers = {
                        "Authorization": f"Bearer {sa_token}",
                        "Content-Type": "application/strategic-merge-patch+json",
                    }
                    patch = {"data": {
                        "tls.crt": base64.b64encode(cert_pem.encode()).decode(),
                        "tls.key": base64.b64encode(key_pem.encode()).decode(),
                    }}
                    k8s_resp = await client.patch(
                        f"https://kubernetes.default.svc/api/v1/namespaces/{k8s_namespace}/secrets/{k8s_secret_name}",
                        headers=k8s_headers,
                        json=patch,
                    )
                    result["k8s_secret_updated"] = k8s_resp.status_code in (200, 201)
                    result["k8s_secret"] = f"{k8s_namespace}/{k8s_secret_name}"
                    if k8s_resp.status_code not in (200, 201):
                        result["k8s_error"] = f"K8s PATCH returned {k8s_resp.status_code}: {k8s_resp.text[:200]}"
                except Exception as ke:
                    result["k8s_error"] = str(ke)

            # 3. Store audit record in Vault KV.
            await client.post(
                f"{_VAULT_ADDR}/v1/secret/data/payments/tls",
                headers=headers,
                json={"data": {"serial": serial, "common_name": common_name,
                               "renewed_at": datetime.datetime.utcnow().isoformat()}},
            )

            result["message"] = (
                f"Cert renewed from Vault (serial {serial}, TTL {ttl}). "
                + (f"K8s Secret {k8s_namespace}/{k8s_secret_name} updated." if k8s_secret_name else "")
            )
            return result

    except httpx.HTTPError as e:
        return {"error": f"Vault rotation failed: {e}"}


async def process_tool_call(
    tool_name: str, arguments: Dict[str, Any], backend_url: str, timeout: float = 10.0
) -> Dict[str, Any]:
    """Execute a tool call by fetching live data from the backend.

    Each tool maps to a pod query on the backend (see backend HandleAgentFetch).
    Returns the fetched data, or an error dict the model can reason about — a
    failed tool call must not crash the agent's loop.
    """
    known = {t["name"] for t in TOOLS}
    if tool_name not in known:
        return {"error": f"Unknown tool: {tool_name}"}

    # Vault tools are handled locally (call Vault HTTP API directly).
    if tool_name == "get_vault_cert_status":
        return await _vault_get_cert_status(
            pki_role=arguments.get("pki_role", ""),
            kv_path=arguments.get("kv_path", ""),
        )
    if tool_name == "rotate_vault_cert":
        return await _vault_rotate_cert(
            pki_role=arguments.get("pki_role", ""),
            common_name=arguments.get("common_name", ""),
            ttl=arguments.get("ttl", "24h"),
            pki_mount=arguments.get("pki_mount", "pki-payments"),
            k8s_secret_name=arguments.get("k8s_secret_name", ""),
            k8s_namespace=arguments.get("k8s_namespace", ""),
        )

    url = f"{backend_url.rstrip('/')}/api/agent/fetch"
    payload = {"tool": tool_name, "args": arguments}

    try:
        async with httpx.AsyncClient(timeout=timeout) as client:
            resp = await client.post(url, json=payload)
            resp.raise_for_status()
            body = resp.json()
    except httpx.HTTPError as exc:
        logger.error("tool fetch failed", extra={"tool": tool_name, "error": str(exc)})
        return {"error": f"backend fetch failed for {tool_name}: {exc}"}

    # body is {"tool": ..., "data": {pod_id: [rows], ...}}
    return body.get("data", {})
