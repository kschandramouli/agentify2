"""Normalize Kubernetes objects into the backend's canonical Event schema.

The event shape mirrors src/backend/internal/models/event.go (Event + EventTraits).
The backend infers the storage backend from the traits (see
context-mesh/policies/storage-strategy.md), so getting the traits right matters
more than the payload contents.
"""

import uuid
from datetime import datetime, timezone
from typing import Any, Optional


def _now_rfc3339() -> str:
    return datetime.now(timezone.utc).isoformat()


def _canonical_event(
    *,
    event_namespace: str,
    event_type: str,
    payload: dict[str, Any],
    traits: dict[str, str],
    entity_key: str,
    text: Optional[str] = None,
    tags: Optional[list[str]] = None,
) -> dict[str, Any]:
    """Assemble a canonical event dict accepted by POST /api/ingest.

    entity_key is the stable identity of the thing the event describes (pod /
    service / secret name). For current-state pods the backend keys storage on it
    so the latest event for an entity overwrites the previous one.
    """
    return {
        "id": str(uuid.uuid4()),
        "timestamp": _now_rfc3339(),
        "event_namespace": event_namespace,
        "type": event_type,
        "source": "kubernetes-api",
        "payload": payload,
        "text": text,
        "traits": traits,
        "entity_key": entity_key,
        "tags": tags or [],
    }


# Traits for current pod/service state: point lookups against the latest value.
_LIVE_STATE_TRAITS = {
    "shape": "structured",
    "access_pattern": "point-lookup",
    "temporality": "current-state",
    "mutability": "mutable",
    "authority": "derived",
    "retention": "ephemeral",
}

# Traits for append-only metric samples: each scrape is retained as its own row so
# a trend (e.g. restarts climbing over time) is readable. Routes to the events
# table via the relational backend for v1 (spec 006, ADR 0013).
_METRIC_SAMPLE_TRAITS = {
    "shape": "numeric/metric",
    "access_pattern": "time-range-scan",
    "temporality": "append-only",
    "mutability": "immutable",
    "authority": "derived",
    "retention": "30d",
}

# Traits for append-only change/deploy events: each rollout is its own row, read
# by time range to correlate a change against a symptom onset (spec 007).
_CHANGE_EVENT_TRAITS = {
    "shape": "structured",
    "access_pattern": "time-range-scan",
    "temporality": "append-only",
    "mutability": "immutable",
    "authority": "derived",
    "retention": "30d",
}


def normalize_pod_event(pod: Any, event_type: str) -> dict[str, Any]:
    """Convert a watch event for a V1Pod into a canonical event."""
    status = pod.status
    payload = {
        "pod_id": pod.metadata.name,
        "namespace": pod.metadata.namespace,
        "phase": status.phase,
        "ready": _is_pod_ready(pod),
        "restarts": _total_restarts(pod),
    }
    return _canonical_event(
        event_namespace="k8fy.live-state",
        event_type=f"pod_{event_type.lower()}",
        payload=payload,
        traits=_LIVE_STATE_TRAITS,
        entity_key=pod.metadata.name,
    )


def normalize_service_event(svc: Any, event_type: str) -> dict[str, Any]:
    """Convert a watch event for a V1Service into a canonical event."""
    payload = {
        "service": svc.metadata.name,
        "namespace": svc.metadata.namespace,
        "cluster_ip": svc.spec.cluster_ip,
        "ports": len(svc.spec.ports or []),
    }
    return _canonical_event(
        event_namespace="k8fy.live-state",
        event_type=f"service_{event_type.lower()}",
        payload=payload,
        traits=_LIVE_STATE_TRAITS,
        entity_key=svc.metadata.name,
    )


def normalize_metric_event(
    pod_name: str, namespace: str, container: str, restarts: int
) -> dict[str, Any]:
    """Convert a scraped restart count into an append-only metric sample.

    Emitted to k8fy.metrics (append-only) rather than k8fy.live-state, so each
    scrape is retained as a distinct row and the restart-count trend over time is
    readable (spec 006). The backend keys the row on the event id, not entity_key,
    so samples accumulate instead of overwriting.
    """
    payload = {
        "pod_id": pod_name,
        "namespace": namespace,
        "container": container,
        "restarts": restarts,
    }
    return _canonical_event(
        event_namespace="k8fy.metrics",
        event_type="pod_metrics",
        payload=payload,
        traits=_METRIC_SAMPLE_TRAITS,
        entity_key=f"{pod_name}/{container}",
    )


def normalize_deploy_event(deployment: Any, revision: str) -> dict[str, Any]:
    """Convert a Deployment rollout (revision change) into an append-only event.

    Emitted to k8fy.events so diagnosis can align a rollout time with a symptom
    onset (spec 007). This is a change record, not current state — every revision
    is retained as its own row.
    """
    spec = deployment.spec
    containers = (spec.template.spec.containers or []) if spec and spec.template else []
    payload = {
        "deployment": deployment.metadata.name,
        "namespace": deployment.metadata.namespace,
        "revision": revision,
        "images": [c.image for c in containers if c.image],
        "replicas_desired": spec.replicas if spec else None,
        "change": "rollout",
    }
    return _canonical_event(
        event_namespace="k8fy.events",
        event_type="deploy",
        payload=payload,
        traits=_CHANGE_EVENT_TRAITS,
        entity_key=deployment.metadata.name,
    )


def normalize_certificate_event(
    secret_name: str, namespace: str, expires_at: Optional[datetime]
) -> dict[str, Any]:
    """Convert a TLS secret's expiry into a canonical event."""
    days_until_expiry: Optional[int] = None
    expires_iso: Optional[str] = None
    if expires_at is not None:
        expires_iso = expires_at.astimezone(timezone.utc).isoformat()
        days_until_expiry = (expires_at.astimezone(timezone.utc) - datetime.now(timezone.utc)).days

    payload = {
        "secret": secret_name,
        "namespace": namespace,
        "expires_at": expires_iso,
        "days_until_expiry": days_until_expiry,
        "should_renew": days_until_expiry is not None and days_until_expiry < 30,
    }
    return _canonical_event(
        event_namespace="k8fy.certificates",
        event_type="cert_check",
        payload=payload,
        traits={**_LIVE_STATE_TRAITS, "retention": "30d"},
        entity_key=secret_name,
    )


def _total_restarts(pod: Any) -> int:
    statuses = pod.status.container_statuses or []
    return sum(cs.restart_count for cs in statuses)


def _is_pod_ready(pod: Any) -> bool:
    for condition in pod.status.conditions or []:
        if condition.type == "Ready":
            return condition.status == "True"
    return False
