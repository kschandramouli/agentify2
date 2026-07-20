"""action_executor.py — Phase-3 write actions (ADR 0020 / spec 011 Use Cases 1+2).

These functions are the ONLY code path that ever writes to a K8s Deployment.
They are never registered as Claude-callable tools — not in TOOLS (tools.py),
not on IncidentResponderSkill's or DeploymentGuardianSkill's tool list, not on
the general chat agent's. The only caller is RemediationExecutorSkill, which
is reached exclusively via the deterministic `execute_remediation` intent
after a human has approved a pending proposal (see ADR 0020). No LLM call sits
between "approved" and "executed".

Each function follows the same in-cluster service-account-token PATCH pattern
already used by `_vault_rotate_cert` in tools.py.
"""

import datetime
import logging
from typing import Any, Dict

import httpx

logger = logging.getLogger(__name__)

_SA_TOKEN_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/token"
_K8S_API = "https://kubernetes.default.svc"


def _k8s_headers(content_type: str) -> Dict[str, str]:
    try:
        with open(_SA_TOKEN_PATH) as f:
            token = f.read()
    except OSError:
        return {}
    return {"Authorization": f"Bearer {token}", "Content-Type": content_type}


async def restart_deployment(namespace: str, deployment: str) -> Dict[str, Any]:
    """Trigger a rolling restart — the same technique `kubectl rollout restart`
    uses: patch a timestamp annotation onto the pod template so K8s rolls new pods."""
    headers = _k8s_headers("application/strategic-merge-patch+json")
    if not headers:
        return {"error": "service account token unavailable — action_executor requires in-cluster credentials"}

    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    patch = {"spec": {"template": {"metadata": {"annotations": {
        "kubectl.kubernetes.io/restartedAt": now,
    }}}}}
    url = f"{_K8S_API}/apis/apps/v1/namespaces/{namespace}/deployments/{deployment}"
    try:
        async with httpx.AsyncClient(timeout=15.0, verify=False) as client:
            resp = await client.patch(url, headers=headers, json=patch)
        if resp.status_code not in (200, 201):
            return {"error": f"restart failed ({resp.status_code}): {resp.text[:300]}"}
        return {"status": "restarted", "namespace": namespace, "deployment": deployment, "restarted_at": now}
    except httpx.HTTPError as e:
        return {"error": f"restart request failed: {e}"}


async def scale_deployment(namespace: str, deployment: str, replicas: int) -> Dict[str, Any]:
    """Patch the Deployment's /scale subresource to the requested replica count."""
    headers = _k8s_headers("application/merge-patch+json")
    if not headers:
        return {"error": "service account token unavailable — action_executor requires in-cluster credentials"}
    if replicas < 0:
        return {"error": f"invalid replica count: {replicas}"}

    patch = {"spec": {"replicas": replicas}}
    url = f"{_K8S_API}/apis/apps/v1/namespaces/{namespace}/deployments/{deployment}/scale"
    try:
        async with httpx.AsyncClient(timeout=15.0, verify=False) as client:
            resp = await client.patch(url, headers=headers, json=patch)
        if resp.status_code not in (200, 201):
            return {"error": f"scale failed ({resp.status_code}): {resp.text[:300]}"}
        return {"status": "scaled", "namespace": namespace, "deployment": deployment, "replicas": replicas}
    except httpx.HTTPError as e:
        return {"error": f"scale request failed: {e}"}


async def rollback_deployment(namespace: str, deployment: str, backend_url: str) -> Dict[str, Any]:
    """Roll back to the previous deploy's recorded container images.

    MVP rollback: replays the images[] recorded on the prior k8fy.events deploy
    row (spec 007) rather than a full K8s ReplicaSet-revision rollback — this
    reuses data agentify already ingests instead of requiring new `replicasets`
    RBAC. Sufficient for image-bump deploys; a fast-follow if change-history
    ever proves insufficient (see ADR 0020).
    """
    from k8fy.tools import process_tool_call  # local import: avoids a circular import at module load

    history = await process_tool_call(
        "get_change_history",
        {"namespace": namespace, "deployment": deployment, "order": "desc", "limit": 2},
        backend_url,
    )
    events = history.get("k8fy.events") or []
    if not isinstance(events, list) or len(events) < 2:
        found = len(events) if isinstance(events, list) else 0
        return {"error": f"not enough change history for {namespace}/{deployment} to determine a prior state (found {found} event(s))"}

    # events[0] is the current deploy; events[1] is the state to roll back to.
    prior = events[1].get("payload", events[1]) if isinstance(events[1], dict) else {}
    images = prior.get("images") or []
    if not images:
        return {"error": "prior change-history event has no recorded images — cannot roll back"}

    headers = _k8s_headers("application/strategic-merge-patch+json")
    if not headers:
        return {"error": "service account token unavailable — action_executor requires in-cluster credentials"}

    url = f"{_K8S_API}/apis/apps/v1/namespaces/{namespace}/deployments/{deployment}"
    try:
        async with httpx.AsyncClient(timeout=15.0, verify=False) as client:
            get_resp = await client.get(url, headers=headers)
            if get_resp.status_code != 200:
                return {"error": f"could not read current deployment ({get_resp.status_code}): {get_resp.text[:300]}"}
            containers = get_resp.json()["spec"]["template"]["spec"]["containers"]
            if len(containers) != len(images):
                return {"error": f"container count mismatch ({len(containers)} current vs {len(images)} recorded) — refusing to guess image assignment"}

            patch = {"spec": {"template": {"spec": {"containers": [
                {"name": c["name"], "image": img} for c, img in zip(containers, images)
            ]}}}}
            patch_resp = await client.patch(url, headers=headers, json=patch)
            if patch_resp.status_code not in (200, 201):
                return {"error": f"rollback patch failed ({patch_resp.status_code}): {patch_resp.text[:300]}"}
    except httpx.HTTPError as e:
        return {"error": f"rollback request failed: {e}"}

    return {"status": "rolled_back", "namespace": namespace, "deployment": deployment, "images": images}
