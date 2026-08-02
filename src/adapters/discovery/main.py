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

from . import k8s_client
from .config import Config, load_from_env
from .health import serve_health
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


async def _scan_once(cfg: Config) -> None:
    namespaces = await k8s_client.list_namespaces(exclude=set(cfg.namespace_exclude))
    for ns in namespaces:
        try:
            await _scan_namespace(ns, cfg)
        except Exception:
            logger.exception("scan failed for namespace=%s", ns)


async def _run(cfg: Config, shutdown: asyncio.Event) -> None:
    caps = await k8s_client.discover_api_capabilities()
    if caps:
        logger.info("connected to Kubernetes %s", caps.get("gitVersion", "unknown"))

    while not shutdown.is_set():
        logger.info("scan cycle starting")
        await _scan_once(cfg)
        logger.info("scan cycle complete")
        try:
            await asyncio.wait_for(shutdown.wait(), timeout=cfg.scan_interval_seconds)
        except asyncio.TimeoutError:
            pass  # normal: next cycle starts


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
        loop.run_until_complete(_run(cfg, shutdown))
    except KeyboardInterrupt:
        logger.info("agentify-discovery shutting down")
    finally:
        loop.close()


if __name__ == "__main__":
    main()
