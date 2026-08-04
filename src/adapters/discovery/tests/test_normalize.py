"""Tests for normalize.py (ADR 0027, merged from the retired k8fy adapter's
normalizer.py + emitter.py) — pure-function event-shape coverage plus
push_event's bearer/best-effort behavior, mirroring test_inventory.py's
httpx.MockTransport pattern.
"""

import json
from datetime import datetime, timedelta, timezone

import httpx
import pytest

from discovery import normalize

_RealAsyncClient = httpx.AsyncClient


def _client_factory(transport: httpx.MockTransport):
    def factory(*args, **kwargs):
        kwargs.pop("verify", None)
        kwargs["transport"] = transport
        return _RealAsyncClient(**kwargs)
    return factory


# ── normalize_pod_event ──────────────────────────────────────────────────────

def test_normalize_pod_event_ready_and_restarts():
    pod = {
        "metadata": {"name": "payment-worker-abc", "namespace": "payments"},
        "status": {
            "phase": "Running",
            "conditions": [{"type": "Ready", "status": "True"}],
            "containerStatuses": [{"restartCount": 2}, {"restartCount": 1}],
        },
    }
    event = normalize.normalize_pod_event(pod, "MODIFIED")
    assert event["event_namespace"] == "k8fy.live-state"
    assert event["type"] == "pod_modified"
    assert event["entity_key"] == "payment-worker-abc"
    assert event["payload"] == {
        "pod_id": "payment-worker-abc", "namespace": "payments",
        "phase": "Running", "ready": True, "restarts": 3,
    }
    assert event["traits"]["temporality"] == "current-state"


def test_normalize_pod_event_not_ready_when_no_ready_condition():
    pod = {"metadata": {"name": "pending-pod", "namespace": "payments"}, "status": {"phase": "Pending"}}
    event = normalize.normalize_pod_event(pod, "ADDED")
    assert event["payload"]["ready"] is False
    assert event["payload"]["restarts"] == 0


# ── normalize_service_event ──────────────────────────────────────────────────

def test_normalize_service_event():
    svc = {
        "metadata": {"name": "payment-api", "namespace": "payments"},
        "spec": {"clusterIP": "10.0.0.5", "ports": [{"port": 80}, {"port": 443}]},
    }
    event = normalize.normalize_service_event(svc, "ADDED")
    assert event["event_namespace"] == "k8fy.live-state"
    assert event["type"] == "service_added"
    assert event["payload"] == {"service": "payment-api", "namespace": "payments", "cluster_ip": "10.0.0.5", "ports": 2}


# ── normalize_metric_event ───────────────────────────────────────────────────

def test_normalize_metric_event():
    event = normalize.normalize_metric_event("payment-worker-abc", "payments", "app", 5)
    assert event["event_namespace"] == "k8fy.metrics"
    assert event["type"] == "pod_metrics"
    assert event["entity_key"] == "payment-worker-abc/app"
    assert event["payload"]["restarts"] == 5
    assert event["traits"]["temporality"] == "append-only"


# ── normalize_deploy_event ───────────────────────────────────────────────────

def test_normalize_deploy_event():
    deployment = {
        "metadata": {"name": "payment-worker", "namespace": "payments"},
        "spec": {
            "replicas": 3,
            "template": {"spec": {"containers": [{"image": "payment-worker:v2"}]}},
        },
    }
    event = normalize.normalize_deploy_event(deployment, "7")
    assert event["event_namespace"] == "k8fy.events"
    assert event["type"] == "deploy"
    assert event["payload"] == {
        "deployment": "payment-worker", "namespace": "payments", "revision": "7",
        "images": ["payment-worker:v2"], "replicas_desired": 3, "change": "rollout",
    }


# ── normalize_certificate_event ──────────────────────────────────────────────

def test_normalize_certificate_event_with_expiry():
    expires_at = datetime.now(timezone.utc) + timedelta(days=10)
    event = normalize.normalize_certificate_event("tls-cert-prod", "payments", expires_at, ["payment.prod.svc"])
    assert event["event_namespace"] == "k8fy.certificates"
    assert event["payload"]["secret"] == "tls-cert-prod"
    assert event["payload"]["days_until_expiry"] in (9, 10)  # boundary-tolerant
    assert event["payload"]["should_renew"] is True
    assert event["payload"]["dns_names"] == ["payment.prod.svc"]


def test_normalize_certificate_event_no_expiry():
    event = normalize.normalize_certificate_event("unparseable-cert", "payments", None)
    assert event["payload"]["expires_at"] is None
    assert event["payload"]["days_until_expiry"] is None
    assert event["payload"]["should_renew"] is False
    assert event["payload"]["dns_names"] == []


# ── push_event ───────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_push_event_sends_bearer_token_and_event_body(monkeypatch):
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("authorization")
        seen["url"] = str(request.url)
        seen["body"] = request.content
        return httpx.Response(202)

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    event = normalize.normalize_metric_event("pod-a", "payments", "app", 1)
    await normalize.push_event(event, "http://backend", "secret-token")

    assert seen["auth"] == "Bearer secret-token"
    assert seen["url"] == "http://backend/api/ingest"
    assert json.loads(seen["body"])["event_namespace"] == "k8fy.metrics"


@pytest.mark.asyncio
async def test_push_event_degrades_silently_on_error(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    event = normalize.normalize_metric_event("pod-a", "payments", "app", 1)
    await normalize.push_event(event, "http://backend", "secret-token")  # must not raise
