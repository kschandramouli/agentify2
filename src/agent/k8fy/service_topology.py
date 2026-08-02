"""service_topology.py — mine a service dependency graph out of log text
(service-topology brainstorm, Option 2 — see ROADMAP/ADR 0011).

Diagnosis today is scoped to one service at a time: `DiagnoseSkill` has no
way to know that a symptom in `payment-backend` might actually be caused by
`payment-prod`, one hop downstream. Building a full dependency graph properly
means distributed tracing or a service mesh (ADR 0011 already defers both
until "we need cross-service traces") — expensive, and requires instrumenting
every service. This is the cheap middle ground: scan log text already
flowing through `get_logs()` for K8s-DNS-shaped hostnames that match a
namespace's *real* known services, and record those as directed edges with
an evidence count. No new infrastructure, no app instrumentation — just
reading logs that already exist.

Deliberately precision-over-recall: a bare, unqualified service-name mention
(e.g. a log line saying "payment-backend restarted") is NOT enough evidence —
too many words could coincidentally match a real service name. Only mentions
shaped like `<service>.<namespace>` (optionally `.svc.cluster.local`), where
BOTH the service is a real known service in that namespace AND the namespace
segment matches, count as evidence. This is a deliberate, revisitable
precision/recall tradeoff, not an oversight — expect the graph to be sparse
against apps that don't log outbound hostnames, and expect it to say nothing
rather than something wrong.
"""

import logging
import re
from typing import Any, Dict, List, Optional, Set

import httpx

logger = logging.getLogger(__name__)

# <label>.<label> optionally followed by the K8s in-cluster DNS suffix.
# Permissive on purpose — cross-validation against known_services/namespace
# (not this regex) is what keeps false positives out; see module docstring.
_HOSTNAME_RE = re.compile(
    r"\b([a-z0-9](?:[a-z0-9-]*[a-z0-9])?)\.([a-z0-9](?:[a-z0-9-]*[a-z0-9])?)(?:\.svc\.cluster\.local)?\b"
)


def extract_service_mentions(log_text: str, namespace: str, known_services: Set[str]) -> Set[str]:
    """Find candidate `<service>.<namespace>[.svc.cluster.local]` mentions in
    log text. A match only counts if the namespace segment equals `namespace`
    AND the service segment is in `known_services` — real ground truth, not
    just regex-shaped text. Returns the set of validated service names
    (never includes `namespace` itself, never raises on malformed input).
    """
    if not log_text or not known_services:
        return set()

    found: Set[str] = set()
    for service_candidate, namespace_candidate in _HOSTNAME_RE.findall(log_text):
        if namespace_candidate == namespace and service_candidate in known_services:
            found.add(service_candidate)
    return found


async def get_known_services(namespace: str, backend_url: str) -> Set[str]:
    """Real services tracked for this namespace — the ground truth
    `extract_service_mentions` cross-validates candidates against. Wraps the
    existing `GET /admin/tracked` endpoint (entries are `"namespace/service"`
    strings); degrades to an empty set on any failure, same as every other
    best-effort fetch in this codebase.
    """
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            resp = await client.get(f"{backend_url.rstrip('/')}/admin/tracked")
            resp.raise_for_status()
            entries: List[str] = resp.json() or []
    except (httpx.HTTPError, ValueError) as e:
        logger.warning("get_known_services failed for namespace=%s: %s", namespace, e)
        return set()

    services = set()
    for entry in entries:
        ns, _, service = entry.partition("/")
        if ns == namespace and service:
            services.add(service)
    return services


async def upsert_service_dependency(namespace: str, from_service: str, to_service: str, backend_url: str) -> None:
    """Record one piece of evidence for a from->to edge. Best-effort: any
    failure is logged and swallowed — losing one piece of evidence never
    surfaces as a diagnosis error."""
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            resp = await client.post(
                f"{backend_url.rstrip('/')}/api/service-dependencies",
                json={"namespace": namespace, "from_service": from_service, "to_service": to_service},
            )
            resp.raise_for_status()
    except httpx.HTTPError as e:
        logger.warning(
            "upsert_service_dependency failed for %s/%s->%s: %s", namespace, from_service, to_service, e
        )


async def fetch_service_dependencies(namespace: str, backend_url: str) -> List[Dict[str, Any]]:
    """Read the namespace's already-mined graph. Degrades to an empty list on
    any failure — a missing graph should never block diagnosis."""
    try:
        async with httpx.AsyncClient(timeout=5.0) as client:
            resp = await client.get(
                f"{backend_url.rstrip('/')}/api/service-dependencies", params={"namespace": namespace}
            )
            resp.raise_for_status()
            return resp.json() or []
    except (httpx.HTTPError, ValueError) as e:
        logger.warning("fetch_service_dependencies failed for namespace=%s: %s", namespace, e)
        return []


async def mine_service_dependencies(namespace: str, from_service: str, log_text: str, backend_url: str) -> None:
    """Extract validated service mentions from `log_text` and record each as
    an edge `from_service -> mentioned_service`. Best-effort end-to-end —
    never raises; a failed mining pass just means the graph doesn't improve
    this time.
    """
    known_services = await get_known_services(namespace, backend_url)
    if not known_services:
        return
    for to_service in extract_service_mentions(log_text, namespace, known_services):
        if to_service == from_service:
            continue  # not a dependency, just the service mentioning itself
        await upsert_service_dependency(namespace, from_service, to_service, backend_url)
