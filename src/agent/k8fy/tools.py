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
            "Request a new TLS certificate from HashiCorp Vault PKI and store the renewed cert in Vault KV. "
            "The Vault Agent Injector sidecar in the target pods will automatically pick up the new cert. "
            "Only call this when expiry is imminent or when explicitly requested by the operator."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "pki_role": {
                    "type": "string",
                    "description": "Vault PKI role name to issue from (e.g. 'payment-service').",
                },
                "common_name": {
                    "type": "string",
                    "description": "Common name for the new cert (e.g. 'payment.payments.svc.cluster.local').",
                },
                "ttl": {
                    "type": "string",
                    "description": "Desired cert TTL (e.g. '720h' for 30 days). Defaults to role max.",
                    "default": "720h",
                },
                "kv_path": {
                    "type": "string",
                    "description": "Vault KV path to store the renewed cert (e.g. 'secret/data/payments/tls').",
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
    pki_role: str, common_name: str, ttl: str = "720h", kv_path: str = ""
) -> Dict[str, Any]:
    """Issue a new cert from Vault PKI and optionally store in KV."""
    if not _VAULT_ADDR:
        return {"error": "VAULT_ADDR not configured — Vault tools are unavailable."}

    headers = {"X-Vault-Token": _VAULT_TOKEN, "Content-Type": "application/json"} if _VAULT_TOKEN else {}

    try:
        async with httpx.AsyncClient(timeout=15.0) as client:
            resp = await client.post(
                f"{_VAULT_ADDR}/v1/pki/issue/{pki_role}",
                headers=headers,
                json={"common_name": common_name, "ttl": ttl},
            )
            resp.raise_for_status()
            data = resp.json().get("data", {})
            serial = data.get("serial_number", "")
            result = {
                "status": "rotated",
                "pki_role": pki_role,
                "common_name": common_name,
                "serial": serial,
                "ttl": ttl,
                "message": (
                    f"New cert issued (serial {serial}). "
                    "Vault Agent Injector will propagate the new cert to pods automatically."
                ),
            }
            # Store in KV if path provided.
            if kv_path and _VAULT_TOKEN:
                import datetime
                kv_resp = await client.post(
                    f"{_VAULT_ADDR}/v1/{kv_path}",
                    headers=headers,
                    json={
                        "data": {
                            "certificate": data.get("certificate", "") + "\n" + data.get("issuing_ca", ""),
                            "private_key": data.get("private_key", ""),
                            "serial": serial,
                            "renewed_at": datetime.datetime.utcnow().isoformat(),
                        }
                    },
                )
                result["kv_stored"] = kv_resp.status_code == 200
                result["kv_path"] = kv_path
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
            ttl=arguments.get("ttl", "720h"),
            kv_path=arguments.get("kv_path", ""),
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
