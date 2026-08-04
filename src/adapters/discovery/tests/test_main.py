"""Tests for main.py's _service_for_pod and _namespace_service_names — new
logic (not copied from src/agent), so it gets its own coverage.
_service_for_pod matches a pod to the Service that selects it via the same
label-selector semantics K8s itself uses to build Service endpoints.
_namespace_service_names decides which namespaces ROADMAP P18 use case #1's
inventory push considers "active", and (ROADMAP P16 / ADR 0023) carries the
real service names through for the service->cluster registry.
"""

import pytest

from discovery import k8s_client, main
from discovery.config import Config
from discovery.main import _namespace_service_names, _scan_ingress, _service_for_pod


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


# ── _scan_ingress (ROADMAP P18 use case #3) ──────────────────────────────────

def _cfg() -> Config:
    return Config(backend_url="http://backend", collector_token="tok", scan_interval_seconds=60, max_pods_per_namespace=5, log_tail_lines=200)


def _fail_if_called(name):
    async def fn(*args, **kwargs):
        raise AssertionError(f"{name} should not have been called")
    return fn


@pytest.mark.asyncio
async def test_scan_ingress_skips_gateway_and_route_when_caps_absent(monkeypatch):
    monkeypatch.setattr(k8s_client, "list_ingresses", lambda ns: _return([]))
    monkeypatch.setattr(k8s_client, "list_gateways", _fail_if_called("list_gateways"))
    monkeypatch.setattr(k8s_client, "list_httproutes", _fail_if_called("list_httproutes"))
    monkeypatch.setattr(k8s_client, "list_routes", _fail_if_called("list_routes"))
    monkeypatch.setattr(main, "push_ingress", _fail_if_called("push_ingress"))

    await _scan_ingress(["payments"], _cfg(), None)


@pytest.mark.asyncio
async def test_scan_ingress_calls_gateway_api_when_capability_present(monkeypatch):
    called = {"gateways": False, "httproutes": False}

    async def gateways(ns):
        called["gateways"] = True
        return [{"name": "main-gateway", "listeners": [{"name": "https", "hostname": "shop.example.com", "port": 443}]}]

    async def httproutes(ns):
        called["httproutes"] = True
        return [{"name": "shop-route", "hostnames": [], "parent_refs": [{"name": "main-gateway", "namespace": ns, "section_name": ""}], "backend_services": ["storefront"]}]

    monkeypatch.setattr(k8s_client, "list_ingresses", lambda ns: _return([]))
    monkeypatch.setattr(k8s_client, "list_gateways", gateways)
    monkeypatch.setattr(k8s_client, "list_httproutes", httproutes)
    monkeypatch.setattr(k8s_client, "list_routes", _fail_if_called("list_routes"))

    pushed = {}

    async def fake_push(entries, backend_url, token):
        pushed["entries"] = entries

    monkeypatch.setattr(main, "push_ingress", fake_push)

    await _scan_ingress(["payments"], _cfg(), {"gateway_api": True, "openshift_route": False})

    assert called["gateways"] and called["httproutes"]
    assert pushed["entries"] == [{"namespace": "payments", "kind": "httproute", "name": "shop-route", "host": "shop.example.com", "backend_service": "storefront"}]


@pytest.mark.asyncio
async def test_scan_ingress_calls_openshift_route_when_capability_present(monkeypatch):
    monkeypatch.setattr(k8s_client, "list_ingresses", lambda ns: _return([]))
    monkeypatch.setattr(k8s_client, "list_gateways", _fail_if_called("list_gateways"))
    monkeypatch.setattr(k8s_client, "list_httproutes", _fail_if_called("list_httproutes"))

    async def routes(ns):
        return [{"name": "shop-route", "host": "shop.apps.example.com", "backend_service": "storefront"}]

    monkeypatch.setattr(k8s_client, "list_routes", routes)

    pushed = {}

    async def fake_push(entries, backend_url, token):
        pushed["entries"] = entries

    monkeypatch.setattr(main, "push_ingress", fake_push)

    await _scan_ingress(["payments"], _cfg(), {"gateway_api": False, "openshift_route": True})

    assert pushed["entries"] == [{"namespace": "payments", "kind": "route", "name": "shop-route", "host": "shop.apps.example.com", "backend_service": "storefront"}]


@pytest.mark.asyncio
async def test_scan_ingress_skips_push_when_no_entries(monkeypatch):
    monkeypatch.setattr(k8s_client, "list_ingresses", lambda ns: _return([]))
    monkeypatch.setattr(main, "push_ingress", _fail_if_called("push_ingress"))

    await _scan_ingress(["payments"], _cfg(), None)


async def _return(value):
    return value
