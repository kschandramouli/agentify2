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


class K8sWatcher:
    """Watches pods/services/deployments and scrapes metrics/certificates."""

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
        self._namespace = namespace
        # Last-seen revision per deployment, so we emit only on an actual rollout
        # (Deployment objects also "modify" for status/replica churn).
        self._deploy_revisions: dict[str, str] = {}

    def watch_pods(self) -> None:
        """Stream pod add/modify/delete events and emit them as they arrive."""
        w = watch.Watch()
        while True:
            try:
                for event in w.stream(
                    self._core.list_namespaced_pod, namespace=self._namespace
                ):
                    pod = event["object"]
                    canonical = normalizer.normalize_pod_event(pod, event["type"])
                    self._emitter.emit(canonical)
            except Exception as exc:  # noqa: BLE001 - keep the watch alive on transient errors
                logger.error("pod watch error; retrying", extra={"error": str(exc)})
                time.sleep(2)

    def watch_services(self) -> None:
        """Stream service events and emit them."""
        w = watch.Watch()
        while True:
            try:
                for event in w.stream(
                    self._core.list_namespaced_service, namespace=self._namespace
                ):
                    svc = event["object"]
                    canonical = normalizer.normalize_service_event(svc, event["type"])
                    self._emitter.emit(canonical)
            except Exception as exc:  # noqa: BLE001
                logger.error("service watch error; retrying", extra={"error": str(exc)})
                time.sleep(2)

    def watch_deployments(self) -> None:
        """Stream Deployment events and emit a `deploy` event on each rollout.

        A Deployment object is modified frequently (status/replica updates), so we
        track the revision annotation and emit only when it changes (spec 007).
        """
        if self._apps is None:
            logger.warning("apps_v1 client not configured; deployment watch disabled")
            return
        w = watch.Watch()
        while True:
            try:
                for event in w.stream(
                    self._apps.list_namespaced_deployment, namespace=self._namespace
                ):
                    dep = event["object"]
                    if event["type"] == "DELETED":
                        self._deploy_revisions.pop(dep.metadata.name, None)
                        continue
                    revision = (dep.metadata.annotations or {}).get(_REVISION_ANNOTATION)
                    if not revision:
                        continue
                    name = dep.metadata.name
                    if self._deploy_revisions.get(name) == revision:
                        continue  # no rollout — just status churn
                    self._deploy_revisions[name] = revision
                    self._emitter.emit(normalizer.normalize_deploy_event(dep, revision))
            except Exception as exc:  # noqa: BLE001
                logger.error("deployment watch error; retrying", extra={"error": str(exc)})
                time.sleep(2)

    def scrape_metrics(self, interval: int) -> None:
        """Periodically emit container restart counts."""
        while True:
            try:
                pods = self._core.list_namespaced_pod(namespace=self._namespace)
                for pod in pods.items:
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
        """Periodically emit TLS certificate expiry for kubernetes.io/tls secrets."""
        while True:
            try:
                secrets = self._core.list_namespaced_secret(namespace=self._namespace)
                for secret in secrets.items:
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


def _parse_cert_expiry(cert_b64: str) -> Optional[datetime]:
    """Decode a base64 PEM certificate and return its NotAfter (UTC), or None."""
    try:
        pem_bytes = base64.b64decode(cert_b64)
        cert = x509.load_pem_x509_certificate(pem_bytes)
        # not_valid_after_utc (tz-aware) exists in cryptography >= 42; older
        # releases expose a naive not_valid_after that is defined to be UTC.
        expires = getattr(cert, "not_valid_after_utc", None)
        if expires is None:
            expires = cert.not_valid_after.replace(tzinfo=timezone.utc)
        return expires
    except Exception as exc:  # noqa: BLE001
        logger.warning("failed to parse certificate", extra={"error": str(exc)})
        return None
