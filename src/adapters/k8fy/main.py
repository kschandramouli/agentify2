"""Entry point for the K8fy adapter.

Watches a Kubernetes namespace and emits canonical events to the agentify backend
ingestion endpoint. Runs the watchers/scrapers as daemon threads and blocks.
"""

import logging
import sys
import threading

from kubernetes import client, config as kube_config

from .config import load_from_env
from .emitter import Emitter
from .logserver import LogReader, serve_logs
from .watcher import K8sWatcher

logger = logging.getLogger("agentify.k8fy")


def _configure_logging() -> None:
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(
        logging.Formatter('{"level":"%(levelname)s","logger":"%(name)s","msg":"%(message)s"}')
    )
    root = logging.getLogger()
    root.setLevel(logging.INFO)
    root.addHandler(handler)


def _load_kube_config() -> None:
    """Load in-cluster config, falling back to a local kubeconfig for dev."""
    try:
        kube_config.load_incluster_config()
        logger.info("loaded in-cluster kube config")
    except kube_config.ConfigException:
        kube_config.load_kube_config()
        logger.info("loaded local kube config")


def main() -> None:
    _configure_logging()
    cfg = load_from_env()
    logger.info(
        "k8fy adapter starting",
        extra={"backend_url": cfg.backend_url, "namespace": cfg.namespace},
    )

    _load_kube_config()
    core_v1 = client.CoreV1Api()
    apps_v1 = client.AppsV1Api()
    emitter = Emitter(cfg.ingest_url, cfg.backend_auth_token)
    watcher = K8sWatcher(core_v1, emitter, cfg.namespace, apps_v1=apps_v1)

    # On-demand log server (spec 008 / ADR 0014): the backend calls this to fetch a
    # bounded, redacted log tail at diagnosis time. Logs are never persisted.
    log_reader = LogReader(core_v1, default_namespace=cfg.namespace, max_tail_lines=cfg.max_tail_lines)

    threads = [
        threading.Thread(target=watcher.watch_pods, name="watch-pods", daemon=True),
        threading.Thread(target=watcher.watch_services, name="watch-services", daemon=True),
        threading.Thread(target=watcher.watch_deployments, name="watch-deployments", daemon=True),
        threading.Thread(
            target=watcher.scrape_metrics, args=(cfg.scrape_interval,),
            name="scrape-metrics", daemon=True,
        ),
        threading.Thread(
            target=watcher.scrape_certificates, args=(cfg.cert_check_interval,),
            name="scrape-certs", daemon=True,
        ),
        threading.Thread(
            target=serve_logs, args=(log_reader, cfg.log_server_port, cfg.adapter_auth_token),
            name="log-server", daemon=True,
        ),
    ]
    for t in threads:
        t.start()

    logger.info("k8fy adapter running")
    # Block the main thread; daemon threads do the work. Exit on Ctrl-C / SIGINT.
    try:
        for t in threads:
            t.join()
    except KeyboardInterrupt:
        logger.info("k8fy adapter shutting down")


if __name__ == "__main__":
    main()
