"""Tests for main.py's _service_for_pod and _namespace_service_names — new
logic (not copied from src/agent), so it gets its own coverage.
_service_for_pod matches a pod to the Service that selects it via the same
label-selector semantics K8s itself uses to build Service endpoints.
_namespace_service_names decides which namespaces ROADMAP P18 use case #1's
inventory push considers "active", and (ROADMAP P16 / ADR 0023) carries the
real service names through for the service->cluster registry.
"""

import pytest

from discovery import k8s_client
from discovery.main import _namespace_service_names, _service_for_pod


def test_matches_pod_via_selector():
    services = [{"name": "payment-backend", "selector": {"app": "payment-backend"}}]
    assert _service_for_pod({"app": "payment-backend", "pod-template-hash": "abc123"}, services) == "payment-backend"


def test_no_match_returns_none():
    services = [{"name": "payment-backend", "selector": {"app": "payment-backend"}}]
    assert _service_for_pod({"app": "payment-ui"}, services) is None


def test_selector_must_be_fully_satisfied():
    services = [{"name": "payment-backend", "selector": {"app": "payment-backend", "tier": "backend"}}]
    # Missing the "tier" label -> not a match, even though "app" matches.
    assert _service_for_pod({"app": "payment-backend"}, services) is None


def test_empty_selector_never_matches():
    services = [{"name": "manually-managed", "selector": {}}]
    assert _service_for_pod({"app": "anything"}, services) is None


def test_picks_first_matching_service_among_several():
    services = [
        {"name": "payment-ui", "selector": {"app": "payment-ui"}},
        {"name": "payment-backend", "selector": {"app": "payment-backend"}},
    ]
    assert _service_for_pod({"app": "payment-backend"}, services) == "payment-backend"


# ── _namespace_service_names ─────────────────────────────────────────────────

async def _empty(namespace):
    return []


async def _nonempty(namespace):
    return ["something"]


@pytest.mark.asyncio
async def test_namespace_active_via_services_returns_their_names(monkeypatch):
    async def services(namespace):
        return [{"name": "payment-api", "selector": {"app": "payment-api"}}, {"name": "payment-worker", "selector": {}}]

    monkeypatch.setattr(k8s_client, "list_services", services)
    monkeypatch.setattr(k8s_client, "list_deployments", _empty)
    monkeypatch.setattr(k8s_client, "list_statefulsets", _empty)
    monkeypatch.setattr(k8s_client, "list_daemonsets", _empty)
    assert await _namespace_service_names("payments") == ["payment-api", "payment-worker"]


@pytest.mark.asyncio
async def test_namespace_active_via_daemonset_only_returns_empty_list(monkeypatch):
    monkeypatch.setattr(k8s_client, "list_services", _empty)
    monkeypatch.setattr(k8s_client, "list_deployments", _empty)
    monkeypatch.setattr(k8s_client, "list_statefulsets", _empty)
    monkeypatch.setattr(k8s_client, "list_daemonsets", _nonempty)
    # Active (has a workload) but no Service fronts it -> empty list, not None.
    assert await _namespace_service_names("kube-monitoring") == []


@pytest.mark.asyncio
async def test_namespace_inactive_with_no_workloads_returns_none(monkeypatch):
    monkeypatch.setattr(k8s_client, "list_services", _empty)
    monkeypatch.setattr(k8s_client, "list_deployments", _empty)
    monkeypatch.setattr(k8s_client, "list_statefulsets", _empty)
    monkeypatch.setattr(k8s_client, "list_daemonsets", _empty)
    assert await _namespace_service_names("empty-ns") is None
