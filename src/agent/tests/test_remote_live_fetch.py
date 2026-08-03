"""Tests for the cluster_id remote-relay branch of live diagnostics (ADR
0022 Decision #7 / ROADMAP P18 use case #9): when a live_* tool call
includes cluster_id, it's relayed to the Hub's POST /api/live-fetch instead
of hitting this agent pod's own in-cluster K8s API — same
httpx.MockTransport pattern as test_live_diagnostics.py.
"""

import httpx
import pytest

from k8fy.tools import _dispatch_live_diagnostic, _remote_live_fetch

_RealAsyncClient = httpx.AsyncClient


def _client_factory(transport: httpx.MockTransport):
    def factory(*args, **kwargs):
        kwargs.pop("verify", None)
        kwargs["transport"] = transport
        return _RealAsyncClient(**kwargs)
    return factory


@pytest.mark.asyncio
async def test_remote_live_fetch_posts_to_live_fetch_endpoint(monkeypatch):
    captured = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["body"] = request.content
        return httpx.Response(200, json={"namespace": "payments", "pods": []})

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    result = await _remote_live_fetch("http://backend", "cluster-42", "live_list_pods", {"namespace": "payments"})

    assert captured["url"] == "http://backend/api/live-fetch"
    assert b"cluster-42" in captured["body"]
    assert result == {"namespace": "payments", "pods": []}


@pytest.mark.asyncio
async def test_remote_live_fetch_strips_cluster_id_from_forwarded_args(monkeypatch):
    captured = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["body"] = request.content
        return httpx.Response(200, json={})

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    await _remote_live_fetch("http://backend", "cluster-42", "live_list_pods", {"namespace": "payments", "cluster_id": "cluster-42"})

    assert b'"args":{"namespace":"payments"}' in captured["body"].replace(b" ", b"")


@pytest.mark.asyncio
async def test_remote_live_fetch_returns_error_on_non_200(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(502, text="cluster not connected")

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    result = await _remote_live_fetch("http://backend", "cluster-42", "live_list_pods", {})

    assert "error" in result


@pytest.mark.asyncio
async def test_dispatch_live_diagnostic_relays_when_cluster_id_present(monkeypatch):
    async def fake_remote(backend_url, cluster_id, tool_name, arguments):
        return {"relayed": True, "cluster_id": cluster_id}

    monkeypatch.setattr("k8fy.tools._remote_live_fetch", fake_remote)

    result = await _dispatch_live_diagnostic("live_list_pods", {"namespace": "payments", "cluster_id": "cluster-42"}, "http://backend")

    assert result == {"relayed": True, "cluster_id": "cluster-42"}


@pytest.mark.asyncio
async def test_dispatch_live_diagnostic_stays_local_without_cluster_id(monkeypatch):
    called = {"remote": False}

    async def fake_remote(*args, **kwargs):
        called["remote"] = True
        return {}

    async def fake_local_list_pods(namespace):
        return {"namespace": namespace, "pods": []}

    monkeypatch.setattr("k8fy.tools._remote_live_fetch", fake_remote)
    monkeypatch.setattr("k8fy.tools.live_diagnostics.live_list_pods", fake_local_list_pods)

    result = await _dispatch_live_diagnostic("live_list_pods", {"namespace": "payments"}, "http://backend")

    assert called["remote"] is False
    assert result == {"namespace": "payments", "pods": []}
