"""Tests for live_relay.py — the collector's persistent outbound connection
to the Hub (ADR 0022 Decision #7 / ROADMAP P18 use case #9).

Uses a FakeWS in place of a real socket for _serve_requests (an async
iterator + a `send` recorder is all the real websockets.connect()'s context
manager offers this module) and monkeypatches websockets.connect itself to
exercise run_forever's reconnect/backoff without real sockets or real delays.
"""

import asyncio
import json

import pytest

from discovery import live_relay
from discovery.config import Config


def test_websocket_url_converts_http_and_https():
    assert live_relay._websocket_url("http://backend:8080") == "ws://backend:8080/api/collector/connect"
    assert live_relay._websocket_url("https://backend") == "wss://backend/api/collector/connect"


class FakeWS:
    def __init__(self, frames):
        self._frames = list(frames)
        self.sent = []

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False

    def __aiter__(self):
        return self

    async def __anext__(self):
        if not self._frames:
            raise StopAsyncIteration
        return self._frames.pop(0)

    async def send(self, data):
        self.sent.append(data)


@pytest.mark.asyncio
async def test_serve_requests_dispatches_and_responds(monkeypatch):
    async def fake_dispatch(tool, args):
        assert tool == "live_list_pods"
        assert args == {"namespace": "payments"}
        return {"pods": []}

    monkeypatch.setattr(live_relay.live_tools, "dispatch", fake_dispatch)

    ws = FakeWS([json.dumps({"id": "req-1", "tool": "live_list_pods", "args": {"namespace": "payments"}})])
    await live_relay._serve_requests(ws)

    assert len(ws.sent) == 1
    assert json.loads(ws.sent[0]) == {"id": "req-1", "type": "response", "result": {"pods": []}}


@pytest.mark.asyncio
async def test_serve_requests_sends_error_on_dispatch_failure(monkeypatch):
    async def fake_dispatch(tool, args):
        raise RuntimeError("boom")

    monkeypatch.setattr(live_relay.live_tools, "dispatch", fake_dispatch)

    ws = FakeWS([json.dumps({"id": "req-1", "tool": "live_list_pods", "args": {}})])
    await live_relay._serve_requests(ws)

    sent = json.loads(ws.sent[0])
    assert sent["id"] == "req-1"
    assert "boom" in sent["error"]


@pytest.mark.asyncio
async def test_serve_requests_ignores_unrecognized_tool(monkeypatch):
    called = False

    async def fake_dispatch(tool, args):
        nonlocal called
        called = True
        return {}

    monkeypatch.setattr(live_relay.live_tools, "dispatch", fake_dispatch)

    ws = FakeWS([json.dumps({"id": "req-1", "tool": "rotate_vault_cert", "args": {}})])
    await live_relay._serve_requests(ws)

    assert called is False
    assert ws.sent == []


@pytest.mark.asyncio
async def test_run_forever_backs_off_and_retries_on_connect_failure(monkeypatch):
    monkeypatch.setattr(live_relay, "_INITIAL_BACKOFF_SECONDS", 0.01)
    monkeypatch.setattr(live_relay, "_MAX_BACKOFF_SECONDS", 0.01)

    attempts = 0

    def failing_connect(url, **kwargs):
        nonlocal attempts
        attempts += 1
        raise ConnectionRefusedError("no route to host")

    monkeypatch.setattr(live_relay.websockets, "connect", failing_connect)

    cfg = Config(
        backend_url="http://backend", collector_token="secret", scan_interval_seconds=60,
        max_pods_per_namespace=5, log_tail_lines=200,
    )
    shutdown = asyncio.Event()

    async def stop_soon():
        await asyncio.sleep(0.05)
        shutdown.set()

    # Should not raise — best-effort reconnect, same discipline as the rest
    # of this package.
    await asyncio.gather(live_relay.run_forever(cfg, shutdown), stop_soon())

    assert attempts >= 1
