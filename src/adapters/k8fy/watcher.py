"""Watch Kubernetes resources and emit canonical events to the backend."""

import base64
import logging
import time
from datetime import datetime, timezone
from typing import Optional

from cryptography import x509
from kubernetes import client, watch

from . import normalizer
from .emitter import Emitter

logger = logging.getLogger("agentify.k8fy.watcher")

_REVISION_ANNOTATION = "deployment.kubernetes.io/revision"

# System namespaces to skip during cluster-wide watching — they produce
# a lot of noise and are never K8fy-relevant service traffic.
_SYSTEM_NAMESPACES = frozenset({
    "kube-system", "kube-public", "kube-node-lease",
    "cert-manager", "monitoring", "ingress-nginx",
})


class K8sWatcher:
    """Watches pods/services/deployments and scrapes metrics/certificates.

    When namespace is empty or '*', operates in cluster-wide mode — watches
    every non-system namespace automatically. New namespaces are picked up
    without any restart.
    """

    def __init__(
        self,
        core_v1: client.CoreV1Api,
        emitter: Emitter,
        namespace: str,
        apps_v1: Optional[client.AppsV1Api] = None,
    ):
        self._core = core_v1
        self._apps = apps_v1
        self._emitter = emitter
        # Empty string or '*' → cluster-wide mode
        self._namespace = namespace if namespace and namespace != "*" else ""
        self._deploy_revisions: dict[str, str] = {}
        mode = f"namespace={self._namespace}" if self._namespace else "cluster-wide"
        logger.info("watcher initialised", extra={"mode": mode})

    @property
    def _cluster_wide(self) -> bool:
        return self._namespace == ""

    # ── Watch streams ──────────────────────────────────────────────────────────

    def watch_pods(self) -> None:
        """Stream pod add/modify/delete events across all (or one) namespace(s)."""
        w = watch.Watch()
        while True:
            try:
                fn = (self._core.list_pod_for_all_namespaces if self._cluster_wide
                      else self._core.list_namespaced_pod)
                kwargs = {} if self._cluster_wide else {"namespace": self._namespace}
                for event in w.stream(fn, **kwargs):
                    pod = event["object"]
                    if self._cluster_wide and self._skip_namespace(pod.metadata.namespace):
                        continue
                    canonical = normalizer.normalize_pod_event(pod, event["type"])
                    self._emitter.emit(canonical)
            except Exception as exc:  # noqa: BLE001
                logger.error("pod watch error; retrying", extra={"error": str(exc)})
                time.sleep(2)

    def watch_services(self) -> None:
        """Stream service events."""
        w = watch.Watch()
        while True:
            try:
                fn = (self._core.list_service_for_all_namespaces if self._cluster_wide
                      else self._core.list_namespaced_service)
                kwargs = {} if self._cluster_wide else {"namespace": self._namespace}
                for event in w.stream(fn, **kwargs):
                    svc = event["object"]
                    if self._cluster_wide and self._skip_namespace(svc.metadata.namespace):
                        continue
                    canonical = normalizer.normalize_service_event(svc, event["type"])
                    self._emitter.emit(canonical)
            except Exception as exc:  # noqa: BLE001
                logger.error("service watch error; retrying", extra={"error": str(exc)})
                time.sleep(2)

    def watch_deployments(self) -> None:
        """Stream Deployment rollout events (spec 007)."""
        if self._apps is None:
            logger.warning("apps_v1 client not configured; deployment watch disabled")
            return
        w = watch.Watch()
        while True:
            try:
                fn = (self._apps.list_deployment_for_all_namespaces if self._cluster_wide
                      else self._apps.list_namespaced_deployment)
                kwargs = {} if self._cluster_wide else {"namespace": self._namespace}
                for event in w.stream(fn, **kwargs):
                    dep = event["object"]
                    if self._cluster_wide and self._skip_namespace(dep.metadata.namespace):
                        continue
                    if event["type"] == "DELETED":
                        self._deploy_revisions.pop(
                            f"{dep.metadata.namespace}/{dep.metadata.name}", None)
                        continue
                    revision = (dep.metadata.annotations or {}).get(_REVISION_ANNOTATION)
                    if not revision:
                        continue
                    key = f"{dep.metadata.namespace}/{dep.metadata.name}"
                    if self._deploy_revisions.get(key) == revision:
                        continue
                    self._deploy_revisions[key] = revision
                    self._emitter.emit(normalizer.normalize_deploy_event(dep, revision))
            except Exception as exc:  # noqa: BLE001
                logger.error("deployment watch error; retrying", extra={"error": str(exc)})
                time.sleep(2)

    # ── Scrape loops ───────────────────────────────────────────────────────────

    def scrape_metrics(self, interval: int) -> None:
        """Periodically emit container restart counts."""
        while True:
            try:
                if self._cluster_wide:
                    pods = self._core.list_pod_for_all_namespaces()
                else:
                    pods = self._core.list_namespaced_pod(namespace=self._namespace)
                for pod in pods.items:
                    if self._cluster_wide and self._skip_namespace(pod.metadata.namespace):
                        continue
                    for cs in pod.status.container_statuses or []:
                        canonical = normalizer.normalize_metric_event(
                            pod.metadata.name,
                            pod.metadata.namespace,
                            cs.name,
                            cs.restart_count,
                        )
                        self._emitter.emit(canonical)
            except Exception as exc:  # noqa: BLE001
                logger.error("metrics scrape error", extra={"error": str(exc)})
            time.sleep(interval)

    def scrape_certificates(self, interval: int) -> None:
        """Periodically emit TLS certificate expiry."""
        while True:
            try:
                if self._cluster_wide:
                    secrets = self._core.list_secret_for_all_namespaces()
                else:
                    secrets = self._core.list_namespaced_secret(namespace=self._namespace)
                for secret in secrets.items:
                    if self._cluster_wide and self._skip_namespace(secret.metadata.namespace):
                        continue
                    if secret.type != "kubernetes.io/tls":
                        continue
                    cert_b64 = (secret.data or {}).get("tls.crt")
                    if not cert_b64:
                        continue
                    expires_at = _parse_cert_expiry(cert_b64)
                    canonical = normalizer.normalize_certificate_event(
                        secret.metadata.name, secret.metadata.namespace, expires_at
                    )
                    self._emitter.emit(canonical)
            except Exception as exc:  # noqa: BLE001
                logger.error("certificate scrape error", extra={"error": str(exc)})
            time.sleep(interval)

    # ── Namespace discovery ────────────────────────────────────────────────────

    def list_namespaces(self) -> list[dict]:
        """Return all non-system namespaces with their service count.

        Used by the namespace sync endpoint to populate the frontend autocomplete
        with namespaces that have no traffic yet.
        """
        try:
            ns_list = self._core.list_namespace()
        except Exception as exc:  # noqa: BLE001
            logger.error("failed to list namespaces", extra={"error": str(exc)})
            return []

        result = []
        for ns in ns_list.items:
            name = ns.metadata.name
            if name in _SYSTEM_NAMESPACES:
                continue
            phase = ns.status.phase if ns.status else "Unknown"
            if phase != "Active":
                continue
            try:
                svcs = self._core.list_namespaced_service(namespace=name)
                svc_names = [s.metadata.name for s in svcs.items]
            except Exception:  # noqa: BLE001
                svc_names = []
            result.append({
                "namespace": name,
                "services": svc_names,
                "service_count": len(svc_names),
            })
        return result

    # ── Helpers ────────────────────────────────────────────────────────────────

    def _skip_namespace(self, ns: str) -> bool:
        return ns in _SYSTEM_NAMESPACES


def _parse_cert_expiry(cert_b64: str) -> Optional[datetime]:
    """Decode a base64 PEM certificate and return its NotAfter (UTC), or None."""
    try:
        pem_bytes = base64.b64decode(cert_b64)
        cert = x509.load_pem_x509_certificate(pem_bytes)
        expires = getattr(cert, "not_valid_after_utc", None)
        if expires is None:
            expires = cert.not_valid_after.replace(tzinfo=timezone.utc)
        return expires
    except Exception as exc:  # noqa: BLE001
        logger.warning("failed to parse certificate", extra={"error": str(exc)})
        return None
