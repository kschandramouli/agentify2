"""Entry point for agentify-discovery (ADR 0022 / ROADMAP P18).

A deterministic, non-agentic, per-cluster collector: on each scan cycle it
lists this cluster's namespaces, mines each namespace's pod logs for
K8s-DNS-shaped service-to-service mentions (extract_service_mentions,
adapted from src/agent/k8fy/service_topology.py), and pushes validated
edges to the Hub's tenant-scoped ingest endpoint via push_dependency. One
long-running Deployment per cluster, not a CronJob + separate API server
(ADR 0022 Decision #6) — periodic push only in v1; the outbound-persistent-
connection / on-demand drill-down design (Decision #7) is explicitly
deferred, see the agentify-discovery plan.
"""

import asyncio
import logging
import signal
import sys
import threading
from typing import Any, Dict, List, Optional

from . import k8s_client, live_relay
from .config import Config, load_from_env
from .health import serve_health
from .ingress import build_ingress_entries, build_route_entries, correlate_gateway_routes, push_ingress
from .inventory import push_inventory
from .log_redaction import redact_log_text
from .service_topology import extract_service_mentions, push_dependency

logger = logging.getLogger("agentify.discovery")


def _configure_logging() -> None:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(
        logging.Formatter('{"level":"%(levelname)s","logger":"%(name)s","msg":"%(message)s"}')
    )
    root = logging.getLogger()
    root.setLevel(logging.INFO)
    root.addHandler(handler)


def _service_for_pod(pod_labels: Dict[str, str], services: List[Dict[str, Any]]) -> Optional[str]:
    """Which Service (by name) a pod belongs to, via the same label-selector
    matching K8s itself uses to build Service endpoints — not a pod-name
    heuristic. A Service with an empty selector (e.g. manually-managed
    Endpoints) never matches."""
    for svc in services:
        selector = svc["selector"]
        if selector and all(pod_labels.get(k) == v for k, v in selector.items()):
            return svc["name"]
    return None


async def _scan_namespace(ns: str, cfg: Config) -> None:
    services = await k8s_client.list_services(ns)
    known = {s["name"] for s in services}
    if not known:
        return

    pods = (await k8s_client.list_pods(ns))[: cfg.max_pods_per_namespace]
    for pod in pods:
        from_service = _service_for_pod(pod["labels"], services)
        if not from_service:
            continue  # can't attribute this pod's mentions to an edge without a from_service

        raw_logs = await k8s_client.get_pod_logs(ns, pod["name"], tail_lines=cfg.log_tail_lines)
        if not raw_logs:
            continue
        logs = redact_log_text(raw_logs)

        for to_service in extract_service_mentions(logs, ns, known):
            if to_service == from_service:
                continue  # self-mention, not a dependency
            await push_dependency(ns, from_service, to_service, cfg.backend_url, cfg.collector_token)


async def _namespace_service_names(ns: str) -> Optional[List[str]]:
    """This namespace's service names if it's "active" — has at least one
    Service, Deployment, StatefulSet, or DaemonSet — else None (excludes
    empty namespaces the ServiceAccount can merely list). ROADMAP P18 use
    case #1 only needed the active/inactive bool; ROADMAP P16 / ADR 0023's
    service->cluster registry needs the real names too, which this same scan
    already has on hand via list_services — nothing extra to fetch.
    """
    services = await k8s_client.list_services(ns)
    if services:
        return [s["name"] for s in services]
    if await k8s_client.list_deployments(ns) or await k8s_client.list_statefulsets(ns) or await k8s_client.list_daemonsets(ns):
        return []  # active (has workloads) but no Service fronts them
    return None  # inactive


async def _scan_inventory(namespaces: List[str], cfg: Config) -> None:
    namespace_services: Dict[str, List[str]] = {}
    for ns in namespaces:
        names = await _namespace_service_names(ns)
        if names is not None:
            namespace_services[ns] = names
    if namespace_services:
        await push_inventory(namespace_services, cfg.backend_url, cfg.collector_token)


async def _scan_ingress(namespaces: List[str], cfg: Config, caps: Optional[Dict[str, Any]]) -> None:
    """Ingress/entry-point mapping (ROADMAP P18 use case #3). Ingress needs no
    capability gate (core-ish, tolerates a missing API on its own); Gateway
    API and OpenShift Route are only scanned when discover_api_capabilities
    found the corresponding group, so a cluster without either CRD installed
    never pays for a 404 per namespace per cycle.
    """
    gateway_api = bool(caps and caps.get("gateway_api"))
    openshift_route = bool(caps and caps.get("openshift_route"))

    gateways_by_key: Dict[Any, Dict[str, Any]] = {}
    if gateway_api:
        for ns in namespaces:
            for gw in await k8s_client.list_gateways(ns):
                gateways_by_key[(ns, gw["name"])] = gw

    entries: List[Dict[str, str]] = []
    for ns in namespaces:
        entries.extend(build_ingress_entries(ns, await k8s_client.list_ingresses(ns)))
        if gateway_api:
            entries.extend(correlate_gateway_routes(ns, await k8s_client.list_httproutes(ns), gateways_by_key))
        if openshift_route:
            entries.extend(build_route_entries(ns, await k8s_client.list_routes(ns)))

    if entries:
        await push_ingress(entries, cfg.backend_url, cfg.collector_token)


async def _scan_once(cfg: Config, caps: Optional[Dict[str, Any]]) -> None:
    namespaces = await k8s_client.list_namespaces(exclude=set(cfg.namespace_exclude))
    try:
        await _scan_inventory(namespaces, cfg)
    except Exception:
        logger.exception("inventory scan failed")
    try:
        await _scan_ingress(namespaces, cfg, caps)
    except Exception:
        logger.exception("ingress scan failed")
    for ns in namespaces:
        try:
            await _scan_namespace(ns, cfg)
        except Exception:
            logger.exception("scan failed for namespace=%s", ns)


async def _run(cfg: Config, shutdown: asyncio.Event) -> None:
    caps = await k8s_client.discover_api_capabilities()
    if caps:
        logger.info(
            "connected to Kubernetes %s (gateway_api=%s, openshift_route=%s)",
            caps.get("gitVersion", "unknown"), caps.get("gateway_api"), caps.get("openshift_route"),
        )

    while not shutdown.is_set():
        logger.info("scan cycle starting")
        await _scan_once(cfg, caps)
        logger.info("scan cycle complete")
        try:
            await asyncio.wait_for(shutdown.wait(), timeout=cfg.scan_interval_seconds)
        except asyncio.TimeoutError:
            pass  # normal: next cycle starts


async def _run_all(cfg: Config, shutdown: asyncio.Event) -> None:
    """Runs the periodic scan-and-push loop and the on-demand live-relay
    connection (ADR 0022 Decision #7 / ROADMAP P18 use case #9) as two
    independent background tasks — a drop in one never affects the other.
    Both already stop cleanly once `shutdown` is set.
    """
    await asyncio.gather(_run(cfg, shutdown), live_relay.run_forever(cfg, shutdown))


def main() -> None:
    _configure_logging()
    cfg = load_from_env()
    if not cfg.collector_token:
        logger.warning("COLLECTOR_TOKEN is not set — every push will be rejected with 401")
    logger.info("agentify-discovery starting", extra={"backend_url": cfg.backend_url})

    threading.Thread(target=serve_health, args=(cfg.health_port,), name="health", daemon=True).start()

    loop = asyncio.new_event_loop()
    asyncio.set_event_loop(loop)
    shutdown = asyncio.Event()

    def _handle_sigterm(*_args: Any) -> None:
        # Finish the in-flight scan cycle (CLAUDE.md graceful-shutdown
        # convention); don't abort mid-namespace. The 60s default
        # SCAN_INTERVAL_SECONDS cycle is well under the pod's
        # terminationGracePeriodSeconds, so this always completes in time.
        logger.info("SIGTERM received, finishing current scan cycle before exit")
        loop.call_soon_threadsafe(shutdown.set)

    signal.signal(signal.SIGTERM, _handle_sigterm)

    try:
        loop.run_until_complete(_run_all(cfg, shutdown))
    except KeyboardInterrupt:
        logger.info("agentify-discovery shutting down")
    finally:
        loop.close()


if __name__ == "__main__":
    main()
