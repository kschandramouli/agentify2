"""live_relay.py — the collector's persistent outbound connection to the Hub
(ADR 0022 Decision #7 / ROADMAP P18 use case #9).

Periodic push (service_topology.push_dependency, inventory.push_inventory)
stays plain HTTP POST — it already works and doesn't need this. This module
is purely for on-demand drill-down: the Hub relays a chat-triggered
live_list_pods/live_get_pod_logs/live_get_events/live_describe_pod request
over this connection when an operator asks about a cluster other than the
one the agent pod runs in, and this module answers it via live_tools.py.

Runs alongside main.py's scan-cycle loop as a second background task. Never
raises out of run_forever — a dropped connection just means the Hub reports
"cluster not connected" for on-demand requests until reconnect succeeds;
periodic push (a separate, unrelated connection) is unaffected either way.
"""

import asyncio
import json
import logging
from typing import Any

import websockets

from . import live_tools
from .config import Config

logger = logging.getLogger("agentify.discovery.live_relay")

_INITIAL_BACKOFF_SECONDS = 1.0
_MAX_BACKOFF_SECONDS = 30.0


def _websocket_url(backend_url: str) -> str:
    """Derive the ws(s):// collector-connect URL from the same BACKEND_URL
    the periodic push calls already use."""
    if backend_url.startswith("https://"):
        base = "wss://" + backend_url[len("https://"):]
    elif backend_url.startswith("http://"):
        base = "ws://" + backend_url[len("http://"):]
    else:
        base = backend_url
    return base.rstrip("/") + "/api/collector/connect"


async def _serve_requests(ws: Any) -> None:
    """Read {id, tool, args} frames off `ws` and answer each with
    {id, type: response, result | error} — one connection can serve many
    requests, sequentially, for as long as it stays open."""
    async for raw in ws:
        try:
            frame = json.loads(raw)
        except (TypeError, ValueError):
            logger.warning("live_relay: dropped a non-JSON frame")
            continue

        request_id = frame.get("id")
        tool = frame.get("tool")
        args = frame.get("args") or {}
        if not request_id or tool not in live_tools.LIVE_TOOLS:
            logger.warning("live_relay: dropped a request for unrecognized tool=%r", tool)
            continue

        try:
            result = await live_tools.dispatch(tool, args)
            await ws.send(json.dumps({"id": request_id, "type": "response", "result": result}))
        except Exception as e:
            logger.exception("live_relay: tool %s failed", tool)
            await ws.send(json.dumps({"id": request_id, "type": "response", "error": str(e)}))


async def run_forever(cfg: Config, shutdown: asyncio.Event) -> None:
    """Hold open a persistent outbound connection to the Hub, reconnecting
    with capped exponential backoff on any disconnect. Runs until
    `shutdown` is set.
    """
    if not cfg.collector_token:
        logger.warning("COLLECTOR_TOKEN is not set — the live drill-down connection will be rejected")

    url = _websocket_url(cfg.backend_url)
    backoff = _INITIAL_BACKOFF_SECONDS

    while not shutdown.is_set():
        try:
            headers = {"Authorization": f"Bearer {cfg.collector_token}"}
            async with websockets.connect(url, additional_headers=headers) as ws:
                logger.info("live_relay: connected to %s", url)
                backoff = _INITIAL_BACKOFF_SECONDS
                await _serve_requests(ws)
        except asyncio.CancelledError:
            raise
        except Exception as e:
            logger.warning("live_relay: connection dropped (%s) — retrying in %.1fs", e, backoff)

        if shutdown.is_set():
            break
        try:
            await asyncio.wait_for(shutdown.wait(), timeout=backoff)
        except asyncio.TimeoutError:
            pass  # normal: reconnect now
        backoff = min(backoff * 2, _MAX_BACKOFF_SECONDS)
