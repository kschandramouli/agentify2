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


async def _group_exists(group: str) -> bool:
    """Whether an API group is registered on this cluster (200 = yes, 404 or
    any other non-200 = no). Used to decide whether it's worth calling a
    CRD-based list function at all — Gateway API and OpenShift Route are
    genuinely optional/distribution-specific, unlike core/apps/v1 groups."""
    try:
        resp = await _k8s_get(f"/apis/{group}")
    except RuntimeError:
        return False
    return resp.status_code == 200


async def discover_api_capabilities() -> Optional[Dict[str, Any]]:
    """Best-effort startup capability check (ADR 0022 Decision #6:
    "API-capability discovery at startup — query /version and /apis, never
    assume a fixed surface"). Logged once at startup.

    Also probes for the two optional, distribution-specific API groups
    ingress mapping (ROADMAP P18 use case #3) needs — Gateway API
    (`gateway.networking.k8s.io`) and OpenShift Route (`route.openshift.io`)
    — under `"gateway_api"`/`"openshift_route"` booleans, so main.py's scan
    loop can skip those list calls entirely on a cluster that doesn't have
    them, rather than eating a 404 (and a log line) every namespace, every
    scan cycle. Ingress itself (`networking.k8s.io/v1`) needs no such gate —
    it's been core since K8s 1.19 and every list function already tolerates
    a missing API gracefully via the same 404-returns-[] fallback.
    """
    try:
        resp = await _k8s_get("/version")
    except RuntimeError as e:
        logger.warning("api capability discovery skipped: %s", e)
        return None
    if resp.status_code != 200:
        logger.warning("GET /version failed (%s): %s", resp.status_code, resp.text[:200])
        return None
    caps = resp.json()
    caps["gateway_api"] = await _group_exists("gateway.networking.k8s.io/v1")
    caps["openshift_route"] = await _group_exists("route.openshift.io/v1")
    return caps


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


async def list_tls_secrets(namespace: str) -> List[Dict[str, str]]:
    """List TLS Secrets in `namespace` as `{"name": ..., "tls_crt_b64": ...}`
    (ROADMAP P16/P18 use case unlocked by ADR 0024's live_get_certificates).

    Filtered server-side to `type=kubernetes.io/tls` via a field selector —
    never lists arbitrary Secrets. Returns the still-base64-encoded `tls.crt`
    field only; decoding/parsing (and NEVER returning it downstream) is
    live_tools.py's job, not this thin transport layer's. `tls.key` (the
    private key) is never read here at all — only `tls.crt` is extracted
    from each Secret's data.
    """
    params = {"fieldSelector": "type=kubernetes.io/tls"}
    resp = await _k8s_get(f"/api/v1/namespaces/{quote(namespace)}/secrets", params)
    if resp.status_code != 200:
        logger.warning("list tls secrets failed for namespace=%s (%s): %s", namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    secrets = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        tls_crt_b64 = item.get("data", {}).get("tls.crt", "")
        if not name or not tls_crt_b64:
            continue
        secrets.append({"name": name, "tls_crt_b64": tls_crt_b64})
    return secrets


async def _list_apps_v1_names(namespace: str, resource: str) -> List[str]:
    """Shared list-and-extract-names helper for the apps/v1 workload kinds
    below — identical shape to list_services/list_pods, just against a
    different API group/resource."""
    resp = await _k8s_get(f"/apis/apps/v1/namespaces/{quote(namespace)}/{resource}")
    if resp.status_code != 200:
        logger.warning("list %s failed for namespace=%s (%s): %s", resource, namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    return [name for item in items if (name := item.get("metadata", {}).get("name", ""))]


async def list_deployments(namespace: str) -> List[str]:
    """List Deployment names in `namespace` (apps/v1)."""
    return await _list_apps_v1_names(namespace, "deployments")


async def list_statefulsets(namespace: str) -> List[str]:
    """List StatefulSet names in `namespace` (apps/v1)."""
    return await _list_apps_v1_names(namespace, "statefulsets")


async def list_daemonsets(namespace: str) -> List[str]:
    """List DaemonSet names in `namespace` (apps/v1)."""
    return await _list_apps_v1_names(namespace, "daemonsets")


async def get_pod_logs(namespace: str, pod: str, tail_lines: int = 200) -> str:
    """Fetch a bounded, unredacted tail of a pod's current logs. Callers
    must redact before this text leaves the cluster (see log_redaction.py)."""
    params = {"tailLines": str(max(1, min(tail_lines, 1000)))}
    resp = await _k8s_get(f"/api/v1/namespaces/{quote(namespace)}/pods/{quote(pod)}/log", params)
    if resp.status_code != 200:
        logger.warning("get pod logs failed for %s/%s (%s): %s", namespace, pod, resp.status_code, resp.text[:200])
        return ""
    return resp.text


# ── Ingress/entry-point mapping (ROADMAP P18 use case #3) ───────────────────
# Ingress is core-ish (networking.k8s.io/v1, present since K8s 1.19) and
# needs no capability gate. Gateway API and OpenShift Route are genuinely
# optional/distribution-specific — main.py only calls list_gateways/
# list_httproutes/list_routes when discover_api_capabilities() said the
# corresponding group exists, but every function here still degrades
# gracefully (returns []) on a 404 regardless, same as every list function
# above.

async def list_ingresses(namespace: str) -> List[Dict[str, Any]]:
    """List Ingresses in `namespace` as
    `{"name", "hosts": [...], "backend_services": [...]}` — one flattened
    entry per Ingress object, not per host/backend pair (ingress.py does
    that flattening); `hosts`/`backend_services` are deduplicated, order-
    preserving lists gathered across every rule."""
    resp = await _k8s_get(f"/apis/networking.k8s.io/v1/namespaces/{quote(namespace)}/ingresses")
    if resp.status_code != 200:
        logger.warning("list ingresses failed for namespace=%s (%s): %s", namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    result = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        if not name:
            continue
        spec = item.get("spec", {})
        hosts: List[str] = []
        backends: List[str] = []
        for rule in spec.get("rules", []) or []:
            host = rule.get("host", "")
            if host and host not in hosts:
                hosts.append(host)
            for path in rule.get("http", {}).get("paths", []) or []:
                svc = path.get("backend", {}).get("service", {}).get("name", "")
                if svc and svc not in backends:
                    backends.append(svc)
        default_svc = spec.get("defaultBackend", {}).get("service", {}).get("name", "")
        if default_svc and default_svc not in backends:
            backends.append(default_svc)
        result.append({"name": name, "hosts": hosts, "backend_services": backends})
    return result


async def list_gateways(namespace: str) -> List[Dict[str, Any]]:
    """List Gateway API Gateways in `namespace` as
    `{"name", "listeners": [{"name", "hostname", "port"}]}`. Only called when
    discover_api_capabilities() found the gateway.networking.k8s.io group;
    still returns [] on a 404 regardless, same as every list function here."""
    resp = await _k8s_get(f"/apis/gateway.networking.k8s.io/v1/namespaces/{quote(namespace)}/gateways")
    if resp.status_code != 200:
        logger.warning("list gateways failed for namespace=%s (%s): %s", namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    result = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        if not name:
            continue
        listeners = [
            {"name": l.get("name", ""), "hostname": l.get("hostname", ""), "port": l.get("port", 0)}
            for l in item.get("spec", {}).get("listeners", []) or []
        ]
        result.append({"name": name, "listeners": listeners})
    return result


async def list_httproutes(namespace: str) -> List[Dict[str, Any]]:
    """List Gateway API HTTPRoutes in `namespace` as
    `{"name", "hostnames": [...], "parent_refs": [{"name", "namespace",
    "section_name"}], "backend_services": [...]}`. `parent_refs["namespace"]`
    defaults to `namespace` (this route's own) per the Gateway API spec when
    the route doesn't set one explicitly — resolved here, not left for the
    caller to default."""
    resp = await _k8s_get(f"/apis/gateway.networking.k8s.io/v1/namespaces/{quote(namespace)}/httproutes")
    if resp.status_code != 200:
        logger.warning("list httproutes failed for namespace=%s (%s): %s", namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    result = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        if not name:
            continue
        spec = item.get("spec", {})
        parent_refs = [
            {
                "name": ref.get("name", ""),
                "namespace": ref.get("namespace") or namespace,
                "section_name": ref.get("sectionName", ""),
            }
            for ref in spec.get("parentRefs", []) or []
        ]
        backends: List[str] = []
        for rule in spec.get("rules", []) or []:
            for ref in rule.get("backendRefs", []) or []:
                svc = ref.get("name", "")
                if svc and svc not in backends:
                    backends.append(svc)
        result.append({
            "name": name,
            "hostnames": spec.get("hostnames", []) or [],
            "parent_refs": parent_refs,
            "backend_services": backends,
        })
    return result


async def list_routes(namespace: str) -> List[Dict[str, str]]:
    """List OpenShift Routes in `namespace` as `{"name", "host",
    "backend_service"}` — primary `spec.to.name` only; weighted
    `alternateBackends` routing is out of scope for v1. Only called when
    discover_api_capabilities() found the route.openshift.io group."""
    resp = await _k8s_get(f"/apis/route.openshift.io/v1/namespaces/{quote(namespace)}/routes")
    if resp.status_code != 200:
        logger.warning("list routes failed for namespace=%s (%s): %s", namespace, resp.status_code, resp.text[:200])
        return []
    items = resp.json().get("items", [])
    result = []
    for item in items:
        name = item.get("metadata", {}).get("name", "")
        if not name:
            continue
        spec = item.get("spec", {})
        result.append({
            "name": name,
            "host": spec.get("host", ""),
            "backend_service": spec.get("to", {}).get("name", ""),
        })
    return result
