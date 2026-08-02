"""k8s_client.py — in-cluster Kubernetes API access for agentify-discovery.

Same auth pattern as src/agent/k8fy/k8s_client.py + live_diagnostics.py
(read the mounted service-account token, call
https://kubernetes.default.svc directly over HTTPS with a Bearer header) —
copied rather than imported, see log_redaction.py's docstring for why.

No `kubernetes` client library dependency, deliberately: raw HTTP calls give
this component direct control over API version-skew fallback (ADR 0022
Decision #6), which a versioned client library would fight rather than help
with.
"""

import logging
from typing import Any, Dict, List, Optional
from urllib.parse import quote

import httpx

logger = logging.getLogger(__name__)

_SA_TOKEN_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/token"
K8S_API = "https://kubernetes.default.svc"


def k8s_headers(content_type: str = "application/json") -> Dict[str, str]:
    try:
        with open(_SA_TOKEN_PATH) as f:
            token = f.read()
    except OSError:
        return {}
    return {"Authorization": f"Bearer {token}", "Content-Type": content_type}


async def _k8s_get(path: str, params: Optional[Dict[str, str]] = None) -> httpx.Response:
    headers = k8s_headers()
    if not headers:
        raise RuntimeError("service account token unavailable — agentify-discovery requires in-cluster credentials")
    async with httpx.AsyncClient(timeout=15.0, verify=False) as client:
        return await client.get(f"{K8S_API}{path}", headers=headers, params=params or {})


async def discover_api_capabilities() -> Optional[Dict[str, Any]]:
    """Best-effort startup capability check (ADR 0022 Decision #6:
    "API-capability discovery at startup — query /version and /apis, never
    assume a fixed surface"). Logged once at startup; nothing in v1 branches
    on the result yet — kept thin until a future use case (e.g. ingress
    mapping) actually needs a non-core API group.
    """
    try:
        resp = await _k8s_get("/version")
    except RuntimeError as e:
        logger.warning("api capability discovery skipped: %s", e)
        return None
    if resp.status_code != 200:
        logger.warning("GET /version failed (%s): %s", resp.status_code, resp.text[:200])
        return None
    return resp.json()


async def list_namespaces(exclude: Optional[set] = None) -> List[str]:
    """List every namespace this ServiceAccount can see, minus `exclude`."""
    exclude = exclude or set()
    resp = await _k8s_get("/api/v1/namespaces")
    if resp.status_code != 200:
        logger.warning("list namespaces failed (%s): %s", resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    return [
        name for item in items
        if (name := item.get("metadata", {}).get("name", "")) and name not in exclude
    ]


async def list_services(namespace: str) -> List[Dict[str, Any]]:
    """List Services in `namespace` as `{"name": ..., "selector": {...}}`.
    Service *names* are the ground truth extract_service_mentions cross-
    validates candidates against; each Service's `selector` is how a pod is
    matched back to the service it belongs to (see main.py's
    `_service_for_pod` — the same label-matching semantics K8s itself uses
    to build Service endpoints, not a pod-name-guessing heuristic).

    Queried directly from this cluster's own K8s API rather than the Hub's
    GET /admin/tracked (see the agentify-discovery plan: that endpoint has
    no tenant scoping, so reusing it would leak cross-tenant service names
    into extraction validation).
    """
    resp = await _k8s_get(f"/api/v1/namespaces/{quote(namespace)}/services")
    if resp.status_code != 200:
        logger.warning("list services failed for namespace=%s (%s): %s", namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    services = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        if not name:
            continue
        services.append({"name": name, "selector": item.get("spec", {}).get("selector") or {}})
    return services


async def list_pods(namespace: str) -> List[Dict[str, Any]]:
    """List pods in `namespace` as `{"name": ..., "labels": {...}}`."""
    resp = await _k8s_get(f"/api/v1/namespaces/{quote(namespace)}/pods")
    if resp.status_code != 200:
        logger.warning("list pods failed for namespace=%s (%s): %s", namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    pods = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        if not name:
            continue
        pods.append({"name": name, "labels": item.get("metadata", {}).get("labels") or {}})
    return pods


async def get_pod_logs(namespace: str, pod: str, tail_lines: int = 200) -> str:
    """Fetch a bounded, unredacted tail of a pod's current logs. Callers
    must redact before this text leaves the cluster (see log_redaction.py)."""
    params = {"tailLines": str(max(1, min(tail_lines, 1000)))}
    resp = await _k8s_get(f"/api/v1/namespaces/{quote(namespace)}/pods/{quote(pod)}/log", params)
    if resp.status_code != 200:
        logger.warning("get pod logs failed for %s/%s (%s): %s", namespace, pod, resp.status_code, resp.text[:200])
        return ""
    return resp.text
