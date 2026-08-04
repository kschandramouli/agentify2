"""Tests for ingress.py (ROADMAP P18 use case #3): build_ingress_entries,
build_route_entries, correlate_gateway_routes, and push_ingress.
"""

import httpx
import pytest

from discovery import ingress

_RealAsyncClient = httpx.AsyncClient


def _client_factory(transport: httpx.MockTransport):
    def factory(*args, **kwargs):
        kwargs.pop("verify", None)
        kwargs["transport"] = transport
        return _RealAsyncClient(**kwargs)
    return factory


# ── build_ingress_entries ────────────────────────────────────────────────────

def test_build_ingress_entries_cross_product_of_hosts_and_backends():
    ingresses = [{"name": "shop-ingress", "hosts": ["shop.example.com", "api.example.com"], "backend_services": ["storefront"]}]
    entries = ingress.build_ingress_entries("payments", ingresses)
    assert entries == [
        {"namespace": "payments", "kind": "ingress", "name": "shop-ingress", "host": "shop.example.com", "backend_service": "storefront"},
        {"namespace": "payments", "kind": "ingress", "name": "shop-ingress", "host": "api.example.com", "backend_service": "storefront"},
    ]


def test_build_ingress_entries_no_hosts_still_records_backend():
    ingresses = [{"name": "catch-all", "hosts": [], "backend_services": ["fallback-svc"]}]
    entries = ingress.build_ingress_entries("payments", ingresses)
    assert entries == [{"namespace": "payments", "kind": "ingress", "name": "catch-all", "host": "", "backend_service": "fallback-svc"}]


def test_build_ingress_entries_no_backends_still_records_host():
    ingresses = [{"name": "orphan", "hosts": ["orphan.example.com"], "backend_services": []}]
    entries = ingress.build_ingress_entries("payments", ingresses)
    assert entries == [{"namespace": "payments", "kind": "ingress", "name": "orphan", "host": "orphan.example.com", "backend_service": ""}]


# ── build_route_entries ──────────────────────────────────────────────────────

def test_build_route_entries_passes_through_one_per_route():
    routes = [{"name": "shop-route", "host": "shop.apps.example.com", "backend_service": "storefront"}]
    assert ingress.build_route_entries("payments", routes) == [
        {"namespace": "payments", "kind": "route", "name": "shop-route", "host": "shop.apps.example.com", "backend_service": "storefront"},
    ]


# ── correlate_gateway_routes ─────────────────────────────────────────────────

def test_correlate_same_namespace_parent_ref():
    gateways_by_key = {
        ("payments", "main-gateway"): {"name": "main-gateway", "listeners": [{"name": "https", "hostname": "shop.example.com", "port": 443}]},
    }
    httproutes = [{
        "name": "shop-route", "hostnames": [],
        "parent_refs": [{"name": "main-gateway", "namespace": "payments", "section_name": ""}],
        "backend_services": ["storefront"],
    }]
    entries = ingress.correlate_gateway_routes("payments", httproutes, gateways_by_key)
    assert entries == [{"namespace": "payments", "kind": "httproute", "name": "shop-route", "host": "shop.example.com", "backend_service": "storefront"}]


def test_correlate_cross_namespace_parent_ref_resolves_via_global_map():
    gateways_by_key = {
        ("gateway-infra", "shared-gateway"): {"name": "shared-gateway", "listeners": [{"name": "https", "hostname": "shared.example.com", "port": 443}]},
    }
    httproutes = [{
        "name": "cross-ns-route", "hostnames": [],
        "parent_refs": [{"name": "shared-gateway", "namespace": "gateway-infra", "section_name": ""}],
        "backend_services": ["api-gw"],
    }]
    entries = ingress.correlate_gateway_routes("payments", httproutes, gateways_by_key)
    assert entries == [{"namespace": "payments", "kind": "httproute", "name": "cross-ns-route", "host": "shared.example.com", "backend_service": "api-gw"}]


def test_correlate_section_name_scopes_to_one_listener():
    gateways_by_key = {
        ("payments", "main-gateway"): {"name": "main-gateway", "listeners": [
            {"name": "https", "hostname": "shop.example.com", "port": 443},
            {"name": "internal", "hostname": "internal.example.com", "port": 8443},
        ]},
    }
    httproutes = [{
        "name": "shop-route", "hostnames": [],
        "parent_refs": [{"name": "main-gateway", "namespace": "payments", "section_name": "https"}],
        "backend_services": ["storefront"],
    }]
    entries = ingress.correlate_gateway_routes("payments", httproutes, gateways_by_key)
    assert entries == [{"namespace": "payments", "kind": "httproute", "name": "shop-route", "host": "shop.example.com", "backend_service": "storefront"}]


def test_correlate_unions_listener_hostname_with_routes_own_hostnames():
    gateways_by_key = {
        ("payments", "main-gateway"): {"name": "main-gateway", "listeners": [{"name": "https", "hostname": "listener.example.com", "port": 443}]},
    }
    httproutes = [{
        "name": "shop-route", "hostnames": ["route-own.example.com"],
        "parent_refs": [{"name": "main-gateway", "namespace": "payments", "section_name": ""}],
        "backend_services": ["storefront"],
    }]
    entries = ingress.correlate_gateway_routes("payments", httproutes, gateways_by_key)
    hosts = {e["host"] for e in entries}
    assert hosts == {"listener.example.com", "route-own.example.com"}


def test_correlate_dangling_parent_ref_skipped_route_own_hostname_still_used():
    httproutes = [{
        "name": "orphan-route", "hostnames": ["orphan.example.com"],
        "parent_refs": [{"name": "missing-gateway", "namespace": "payments", "section_name": ""}],
        "backend_services": ["some-svc"],
    }]
    entries = ingress.correlate_gateway_routes("payments", httproutes, {})
    assert entries == [{"namespace": "payments", "kind": "httproute", "name": "orphan-route", "host": "orphan.example.com", "backend_service": "some-svc"}]


def test_correlate_no_hostname_anywhere_still_records_backend():
    gateways_by_key = {
        ("payments", "main-gateway"): {"name": "main-gateway", "listeners": [{"name": "https", "hostname": "", "port": 443}]},
    }
    httproutes = [{
        "name": "no-host-route", "hostnames": [],
        "parent_refs": [{"name": "main-gateway", "namespace": "payments", "section_name": ""}],
        "backend_services": ["storefront"],
    }]
    entries = ingress.correlate_gateway_routes("payments", httproutes, gateways_by_key)
    assert entries == [{"namespace": "payments", "kind": "httproute", "name": "no-host-route", "host": "", "backend_service": "storefront"}]


# ── push_ingress ─────────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_push_ingress_sends_bearer_token_and_entries(monkeypatch):
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("authorization")
        seen["url"] = str(request.url)
        seen["body"] = request.content
        return httpx.Response(204)

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    entries = [{"namespace": "payments", "kind": "ingress", "name": "shop-ingress", "host": "shop.example.com", "backend_service": "storefront"}]
    await ingress.push_ingress(entries, "http://backend", "secret-token")

    assert seen["auth"] == "Bearer secret-token"
    assert seen["url"] == "http://backend/api/cluster-ingress"
    import json
    assert json.loads(seen["body"]) == {"entries": entries}


@pytest.mark.asyncio
async def test_push_ingress_degrades_silently_on_error(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    # Should not raise — best-effort, same convention as push_inventory/push_dependency.
    await ingress.push_ingress([], "http://backend", "secret-token")
