"""Tests for POST /live-tool-call — the direct (non-LLM) invocation endpoint
the Chat UI's "Run" buttons hit.

The load-bearing assertion here is the allow-list: this endpoint must reject
anything outside LIVE_DIAGNOSTIC_TOOLS, so a mutating tool like
rotate_vault_cert can never be reached through it, even by accident.
"""

import pytest
from fastapi.testclient import TestClient

import app as app_module


@pytest.fixture
def client(monkeypatch):
    async def fake_process_tool_call(tool_name, arguments, backend_url, timeout=10.0):
        return {"tool_name": tool_name, "arguments": arguments}

    monkeypatch.setattr(app_module, "process_tool_call", fake_process_tool_call)
    return TestClient(app_module.app)


def test_live_tool_call_allows_known_tool(client):
    resp = client.post("/live-tool-call", json={"tool": "live_list_pods", "arguments": {"namespace": "payments"}})
    assert resp.status_code == 200
    body = resp.json()
    assert body["tool"] == "live_list_pods"
    assert body["data"]["arguments"] == {"namespace": "payments"}


@pytest.mark.parametrize("tool", ["rotate_vault_cert", "get_similar_incidents", "live_delete_pod", "not_a_tool"])
def test_live_tool_call_rejects_disallowed_tool(client, tool):
    resp = client.post("/live-tool-call", json={"tool": tool, "arguments": {}})
    assert resp.status_code == 400
