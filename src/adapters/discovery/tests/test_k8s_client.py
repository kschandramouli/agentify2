"""Tests for k8s_client.py's ingress/entry-point mapping additions
(ROADMAP P18 use case #3): list_ingresses, list_gateways, list_httproutes,
list_routes, and discover_api_capabilities's group-probing.

Same httpx.MockTransport pattern as test_inventory.py/test_service_topology.py,
plus a k8s_headers() monkeypatch so _k8s_get doesn't need a real mounted
service-account token file.
"""

import httpx
import pytest

from discovery import k8s_client

_RealAsyncClient = httpx.AsyncClient


def _client_factory(transport: httpx.MockTransport):
    def factory(*args, **kwargs):
        kwargs.pop("verify", None)
        kwargs["transport"] = transport
        return _RealAsyncClient(**kwargs)
    return factory


@pytest.fixture(autouse=True)
def _fake_sa_token(monkeypatch):
    monkeypatch.setattr(k8s_client, "k8s_headers", lambda content_type="application/json": {"Authorization": "Bearer fake"})


def _mock(monkeypatch, handler):
    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))


# ── list_ingresses ───────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_list_ingresses_flattens_hosts_and_backends(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        assert "/apis/networking.k8s.io/v1/namespaces/payments/ingresses" in str(request.url)
        return httpx.Response(200, json={"items": [{
            "metadata": {"name": "shop-ingress"},
            "spec": {
                "rules": [
                    {"host": "shop.example.com", "http": {"paths": [{"backend": {"service": {"name": "storefront"}}}]}},
                    {"host": "api.example.com", "http": {"paths": [{"backend": {"service": {"name": "api-gw"}}}]}},
                ],
            },
        }]})
    _mock(monkeypatch, handler)

    result = await k8s_client.list_ingresses("payments")
    assert result == [{
        "name": "shop-ingress",
        "hosts": ["shop.example.com", "api.example.com"],
        "backend_services": ["storefront", "api-gw"],
    }]


@pytest.mark.asyncio
async def test_list_ingresses_falls_back_to_default_backend(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"items": [{
            "metadata": {"name": "catch-all"},
            "spec": {"defaultBackend": {"service": {"name": "fallback-svc"}}},
        }]})
    _mock(monkeypatch, handler)

    result = await k8s_client.list_ingresses("payments")
    assert result == [{"name": "catch-all", "hosts": [], "backend_services": ["fallback-svc"]}]


@pytest.mark.asyncio
async def test_list_ingresses_returns_empty_on_404(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(404)
    _mock(monkeypatch, handler)

    assert await k8s_client.list_ingresses("payments") == []


# ── list_pod_health ──────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_list_pod_health_counts_ready_via_ready_condition(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        assert "/api/v1/namespaces/payments/pods" in str(request.url)
        return httpx.Response(200, json={"items": [
            {"metadata": {"name": "pod-a"}, "status": {"conditions": [{"type": "Ready", "status": "True"}]}},
            {"metadata": {"name": "pod-b"}, "status": {"conditions": [{"type": "Ready", "status": "False"}]}},
            {"metadata": {"name": "pod-c"}, "status": {"conditions": [{"type": "PodScheduled", "status": "True"}]}},
        ]})
    _mock(monkeypatch, handler)

    assert await k8s_client.list_pod_health("payments") == {"total": 3, "ready": 1}


@pytest.mark.asyncio
async def test_list_pod_health_missing_conditions_counts_as_not_ready(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"items": [{"metadata": {"name": "pending-pod"}, "status": {}}]})
    _mock(monkeypatch, handler)

    assert await k8s_client.list_pod_health("payments") == {"total": 1, "ready": 0}


@pytest.mark.asyncio
async def test_list_pod_health_empty_namespace(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"items": []})
    _mock(monkeypatch, handler)

    assert await k8s_client.list_pod_health("payments") == {"total": 0, "ready": 0}


@pytest.mark.asyncio
async def test_list_pod_health_returns_zeros_on_error(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(500)
    _mock(monkeypatch, handler)

    assert await k8s_client.list_pod_health("payments") == {"total": 0, "ready": 0}


# ── list_gateways ────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_list_gateways_extracts_listeners(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        assert "/apis/gateway.networking.k8s.io/v1/namespaces/payments/gateways" in str(request.url)
        return httpx.Response(200, json={"items": [{
            "metadata": {"name": "main-gateway"},
            "spec": {"listeners": [{"name": "https", "hostname": "shop.example.com", "port": 443}]},
        }]})
    _mock(monkeypatch, handler)

    result = await k8s_client.list_gateways("payments")
    assert result == [{
        "name": "main-gateway",
        "listeners": [{"name": "https", "hostname": "shop.example.com", "port": 443}],
    }]


@pytest.mark.asyncio
async def test_list_gateways_returns_empty_when_crd_not_installed(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(404)
    _mock(monkeypatch, handler)

    assert await k8s_client.list_gateways("payments") == []


# ── list_httproutes ──────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_list_httproutes_extracts_parent_refs_and_backends(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"items": [{
            "metadata": {"name": "shop-route"},
            "spec": {
                "hostnames": ["shop.example.com"],
                "parentRefs": [{"name": "main-gateway", "sectionName": "https"}],
                "rules": [{"backendRefs": [{"name": "storefront"}]}],
            },
        }]})
    _mock(monkeypatch, handler)

    result = await k8s_client.list_httproutes("payments")
    assert result == [{
        "name": "shop-route",
        "hostnames": ["shop.example.com"],
        "parent_refs": [{"name": "main-gateway", "namespace": "payments", "section_name": "https"}],
        "backend_services": ["storefront"],
    }]


@pytest.mark.asyncio
async def test_list_httproutes_parent_ref_namespace_explicit_override(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"items": [{
            "metadata": {"name": "cross-ns-route"},
            "spec": {"parentRefs": [{"name": "shared-gateway", "namespace": "gateway-infra"}], "rules": []},
        }]})
    _mock(monkeypatch, handler)

    result = await k8s_client.list_httproutes("payments")
    assert result[0]["parent_refs"] == [{"name": "shared-gateway", "namespace": "gateway-infra", "section_name": ""}]


# ── list_routes ──────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_list_routes_extracts_host_and_primary_backend(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        assert "/apis/route.openshift.io/v1/namespaces/payments/routes" in str(request.url)
        return httpx.Response(200, json={"items": [{
            "metadata": {"name": "shop-route"},
            "spec": {"host": "shop.apps.example.com", "to": {"name": "storefront"}},
        }]})
    _mock(monkeypatch, handler)

    result = await k8s_client.list_routes("payments")
    assert result == [{"name": "shop-route", "host": "shop.apps.example.com", "backend_service": "storefront"}]


@pytest.mark.asyncio
async def test_list_routes_returns_empty_on_non_openshift_cluster(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(404)
    _mock(monkeypatch, handler)

    assert await k8s_client.list_routes("payments") == []


# ── discover_api_capabilities group probing ─────────────────────────────────

@pytest.mark.asyncio
async def test_discover_api_capabilities_detects_both_optional_groups(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        url = str(request.url)
        if url.endswith("/version"):
            return httpx.Response(200, json={"gitVersion": "v1.30.0"})
        if "gateway.networking.k8s.io" in url:
            return httpx.Response(200, json={})
        if "route.openshift.io" in url:
            return httpx.Response(404)
        raise AssertionError(f"unexpected URL {url}")
    _mock(monkeypatch, handler)

    caps = await k8s_client.discover_api_capabilities()
    assert caps["gateway_api"] is True
    assert caps["openshift_route"] is False


@pytest.mark.asyncio
async def test_discover_api_capabilities_both_absent_on_vanilla_k8s(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        url = str(request.url)
        if url.endswith("/version"):
            return httpx.Response(200, json={"gitVersion": "v1.30.0"})
        return httpx.Response(404)
    _mock(monkeypatch, handler)

    caps = await k8s_client.discover_api_capabilities()
    assert caps["gateway_api"] is False
    assert caps["openshift_route"] is False
