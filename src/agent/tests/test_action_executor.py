"""Tests for action_executor.py (ADR 0020 / spec 011 Use Cases 1+2).

These are the ONLY functions that ever write to a K8s Deployment; every test
here asserts on the exact PATCH payload sent, since a wrong payload here is a
production incident, not a cosmetic bug. httpx.MockTransport intercepts the
in-cluster API calls — no real cluster or network access required.
"""

import json

import httpx
import pytest

from k8fy import action_executor as ae
from k8fy import k8s_client


_RealAsyncClient = httpx.AsyncClient


def _client_factory(transport: httpx.MockTransport):
    """Returns a drop-in replacement for httpx.AsyncClient that routes every
    request through the given mock transport, ignoring verify=False/timeout."""
    def factory(*args, **kwargs):
        kwargs.pop("verify", None)
        kwargs["transport"] = transport
        return _RealAsyncClient(**kwargs)
    return factory


@pytest.fixture
def sa_token(tmp_path, monkeypatch):
    token_file = tmp_path / "token"
    token_file.write_text("test-token")
    monkeypatch.setattr(k8s_client, "_SA_TOKEN_PATH", str(token_file))


@pytest.mark.asyncio
async def test_restart_deployment_missing_token(monkeypatch):
    monkeypatch.setattr(k8s_client, "_SA_TOKEN_PATH", "/nonexistent/path/token")
    result = await ae.restart_deployment("payments", "payment-worker")
    assert "error" in result


@pytest.mark.asyncio
async def test_restart_deployment_patches_annotation(sa_token, monkeypatch):
    captured = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["method"] = request.method
        captured["url"] = str(request.url)
        captured["auth"] = request.headers.get("authorization")
        captured["body"] = json.loads(request.content)
        return httpx.Response(200, json={"ok": True})

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    result = await ae.restart_deployment("payments", "payment-worker")

    assert result["status"] == "restarted"
    assert captured["method"] == "PATCH"
    assert captured["url"] == "https://kubernetes.default.svc/apis/apps/v1/namespaces/payments/deployments/payment-worker"
    assert captured["auth"] == "Bearer test-token"
    annotations = captured["body"]["spec"]["template"]["metadata"]["annotations"]
    assert "kubectl.kubernetes.io/restartedAt" in annotations


@pytest.mark.asyncio
async def test_restart_deployment_surfaces_k8s_error(sa_token, monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(403, text="Forbidden")

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    result = await ae.restart_deployment("payments", "payment-worker")
    assert "error" in result
    assert "403" in result["error"]


@pytest.mark.asyncio
async def test_scale_deployment_patches_scale_subresource(sa_token, monkeypatch):
    captured = {}

    def handler(request: httpx.Request) -> httpx.Response:
        captured["url"] = str(request.url)
        captured["body"] = json.loads(request.content)
        return httpx.Response(200, json={"ok": True})

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    result = await ae.scale_deployment("payments", "payment-worker", 4)

    assert result == {"status": "scaled", "namespace": "payments", "deployment": "payment-worker", "replicas": 4}
    assert captured["url"] == "https://kubernetes.default.svc/apis/apps/v1/namespaces/payments/deployments/payment-worker/scale"
    assert captured["body"]["spec"]["replicas"] == 4


@pytest.mark.asyncio
async def test_scale_deployment_rejects_negative_replicas(sa_token):
    result = await ae.scale_deployment("payments", "payment-worker", -1)
    assert "error" in result


@pytest.mark.asyncio
async def test_rollback_deployment_replays_prior_images(sa_token, monkeypatch):
    async def fake_process_tool_call(tool_name, args, backend_url, timeout=10.0):
        assert tool_name == "get_change_history"
        assert args["deployment"] == "payment-worker"
        return {
            "k8fy.events": [
                {"payload": {"deployment": "payment-worker", "images": ["payment-worker:v2"]}},
                {"payload": {"deployment": "payment-worker", "images": ["payment-worker:v1"]}},
            ]
        }

    monkeypatch.setattr("k8fy.tools.process_tool_call", fake_process_tool_call)

    captured = {}

    def handler(request: httpx.Request) -> httpx.Response:
        if request.method == "GET":
            return httpx.Response(200, json={
                "spec": {"template": {"spec": {"containers": [{"name": "payment-worker"}]}}}
            })
        captured["body"] = json.loads(request.content)
        return httpx.Response(200, json={"ok": True})

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    result = await ae.rollback_deployment("payments", "payment-worker", "http://backend")

    assert result["status"] == "rolled_back"
    assert result["images"] == ["payment-worker:v1"]
    assert captured["body"]["spec"]["template"]["spec"]["containers"] == [
        {"name": "payment-worker", "image": "payment-worker:v1"}
    ]


@pytest.mark.asyncio
async def test_rollback_deployment_insufficient_history(sa_token, monkeypatch):
    async def fake_process_tool_call(tool_name, args, backend_url, timeout=10.0):
        return {"k8fy.events": [{"payload": {"deployment": "payment-worker", "images": ["v2"]}}]}

    monkeypatch.setattr("k8fy.tools.process_tool_call", fake_process_tool_call)

    result = await ae.rollback_deployment("payments", "payment-worker", "http://backend")
    assert "error" in result


@pytest.mark.asyncio
async def test_rollback_deployment_container_count_mismatch(sa_token, monkeypatch):
    async def fake_process_tool_call(tool_name, args, backend_url, timeout=10.0):
        return {
            "k8fy.events": [
                {"payload": {"deployment": "payment-worker", "images": ["v2"]}},
                {"payload": {"deployment": "payment-worker", "images": ["v1a", "v1b"]}},
            ]
        }

    monkeypatch.setattr("k8fy.tools.process_tool_call", fake_process_tool_call)

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={
            "spec": {"template": {"spec": {"containers": [{"name": "payment-worker"}]}}}
        })

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    result = await ae.rollback_deployment("payments", "payment-worker", "http://backend")
    assert "error" in result
    assert "mismatch" in result["error"]
