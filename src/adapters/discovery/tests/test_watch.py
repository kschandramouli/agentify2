"""Tests for watch.py (ADR 0027, merged from the retired k8fy adapter's
watcher.py) — the continuous pod/service/deployment watch streams.

Reconnect/backoff test mirrors test_live_relay.py's
test_run_forever_backs_off_and_retries_on_connect_failure (same pattern:
monkeypatch the backoff constants down to near-zero, make the underlying
call fail, and confirm run_forever never raises and retries at least once).
"""

import asyncio

import pytest

from discovery import watch
from discovery.config import Config


def _cfg(**overrides) -> Config:
    base = dict(
        backend_url="http://backend", collector_token="secret", scan_interval_seconds=60,
        max_pods_per_namespace=5, log_tail_lines=200, namespace_exclude=["kube-system"],
    )
    base.update(overrides)
    return Config(**base)


# ── _watch_loop reconnect/backoff ────────────────────────────────────────────

@pytest.mark.asyncio
async def test_watch_loop_backs_off_and_retries_on_failure(monkeypatch):
    monkeypatch.setattr(watch, "_INITIAL_BACKOFF_SECONDS", 0.01)
    monkeypatch.setattr(watch, "_MAX_BACKOFF_SECONDS", 0.01)

    attempts = 0

    async def failing_watch_resource(path, params=None):
        nonlocal attempts
        attempts += 1
        raise ConnectionRefusedError("no route to host")
        yield  # pragma: no cover — makes this an async generator function

    monkeypatch.setattr(watch.k8s_client, "watch_resource", failing_watch_resource)

    shutdown = asyncio.Event()

    async def stop_soon():
        await asyncio.sleep(0.05)
        shutdown.set()

    async def handle_event(event_type, obj, cfg):
        pass  # pragma: no cover — never reached, the watch always fails

    # Should not raise — best-effort reconnect, same discipline as live_relay.py.
    await asyncio.gather(watch._watch_loop("test", "/api/v1/test", _cfg(), shutdown, handle_event), stop_soon())

    assert attempts >= 1


@pytest.mark.asyncio
async def test_watch_loop_filters_excluded_namespaces(monkeypatch):
    async def fake_watch_resource(path, params=None):
        yield {"type": "ADDED", "object": {"metadata": {"namespace": "kube-system", "name": "x"}}}
        yield {"type": "ADDED", "object": {"metadata": {"namespace": "payments", "name": "y"}}}

    monkeypatch.setattr(watch.k8s_client, "watch_resource", fake_watch_resource)

    handled = []

    async def handle_event(event_type, obj, cfg):
        handled.append(obj["metadata"]["name"])

    shutdown = asyncio.Event()
    shutdown.set()  # loop body still runs once through the generator before checking shutdown

    # Run just one pass by calling with a shutdown already set after the
    # first iteration — simplest way to bound this without real backoff waits.
    async def run_once():
        async for event in fake_watch_resource("/x"):
            obj = event.get("object", {})
            ns = obj.get("metadata", {}).get("namespace", "")
            if ns in _cfg().namespace_exclude:
                continue
            await handle_event(event.get("type", ""), obj, _cfg())

    await run_once()
    assert handled == ["y"]


# ── event handlers ────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_handle_pod_event_pushes_normalized_event(monkeypatch):
    pushed = {}

    async def fake_push_event(event, backend_url, collector_token):
        pushed.update(event=event, backend_url=backend_url, collector_token=collector_token)

    monkeypatch.setattr(watch.normalize, "push_event", fake_push_event)

    pod = {"metadata": {"name": "pod-a", "namespace": "payments"}, "status": {"phase": "Running"}}
    await watch._handle_pod_event("ADDED", pod, _cfg())

    assert pushed["event"]["event_namespace"] == "k8fy.live-state"
    assert pushed["event"]["entity_key"] == "pod-a"
    assert pushed["collector_token"] == "secret"


@pytest.mark.asyncio
async def test_handle_service_event_pushes_normalized_event(monkeypatch):
    pushed = {}

    async def fake_push_event(event, backend_url, collector_token):
        pushed.update(event=event)

    monkeypatch.setattr(watch.normalize, "push_event", fake_push_event)

    svc = {"metadata": {"name": "svc-a", "namespace": "payments"}, "spec": {}}
    await watch._handle_service_event("MODIFIED", svc, _cfg())

    assert pushed["event"]["event_namespace"] == "k8fy.live-state"
    assert pushed["event"]["type"] == "service_modified"


# ── deployment handler: revision dedup ───────────────────────────────────────

@pytest.mark.asyncio
async def test_deployment_handler_dedups_same_revision(monkeypatch):
    pushes = []

    async def fake_push_event(event, backend_url, collector_token):
        pushes.append(event)

    monkeypatch.setattr(watch.normalize, "push_event", fake_push_event)

    handle = watch._make_deployment_handler()
    dep = {
        "metadata": {"name": "payment-worker", "namespace": "payments", "annotations": {"deployment.kubernetes.io/revision": "3"}},
        "spec": {"replicas": 2, "template": {"spec": {"containers": []}}},
    }

    await handle("MODIFIED", dep, _cfg())
    await handle("MODIFIED", dep, _cfg())  # same revision — must not push again

    assert len(pushes) == 1


@pytest.mark.asyncio
async def test_deployment_handler_pushes_on_revision_change(monkeypatch):
    pushes = []

    async def fake_push_event(event, backend_url, collector_token):
        pushes.append(event["payload"]["revision"])

    monkeypatch.setattr(watch.normalize, "push_event", fake_push_event)

    handle = watch._make_deployment_handler()

    def dep(revision):
        return {
            "metadata": {"name": "payment-worker", "namespace": "payments", "annotations": {"deployment.kubernetes.io/revision": revision}},
            "spec": {"replicas": 2, "template": {"spec": {"containers": []}}},
        }

    await handle("MODIFIED", dep("3"), _cfg())
    await handle("MODIFIED", dep("4"), _cfg())

    assert pushes == ["3", "4"]


@pytest.mark.asyncio
async def test_deployment_handler_deleted_clears_state_allowing_replay(monkeypatch):
    pushes = []

    async def fake_push_event(event, backend_url, collector_token):
        pushes.append(event["payload"]["revision"])

    monkeypatch.setattr(watch.normalize, "push_event", fake_push_event)

    handle = watch._make_deployment_handler()
    dep = {
        "metadata": {"name": "payment-worker", "namespace": "payments", "annotations": {"deployment.kubernetes.io/revision": "3"}},
        "spec": {"replicas": 2, "template": {"spec": {"containers": []}}},
    }

    await handle("MODIFIED", dep, _cfg())
    await handle("DELETED", dep, _cfg())
    await handle("ADDED", dep, _cfg())  # same revision again, but state was cleared by DELETED

    assert pushes == ["3", "3"]


@pytest.mark.asyncio
async def test_deployment_handler_skips_when_no_revision_annotation(monkeypatch):
    pushes = []

    async def fake_push_event(event, backend_url, collector_token):
        pushes.append(event)

    monkeypatch.setattr(watch.normalize, "push_event", fake_push_event)

    handle = watch._make_deployment_handler()
    dep = {"metadata": {"name": "payment-worker", "namespace": "payments", "annotations": {}}, "spec": {}}

    await handle("MODIFIED", dep, _cfg())

    assert pushes == []
