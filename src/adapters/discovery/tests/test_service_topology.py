"""Tests for service_topology.py.

`extract_service_mentions` is copied verbatim from
src/agent/k8fy/service_topology.py — these cases mirror
src/agent/tests/test_service_topology.py's pure-function coverage exactly,
since the logic (and its precision-over-recall guarantees) must not drift
between the two copies. `push_dependency` is new to this package (it adds
the Bearer credential the original upsert_service_dependency never sent),
so it gets its own coverage of that specific behavior.
"""

import httpx
import pytest

from discovery import service_topology as st

_RealAsyncClient = httpx.AsyncClient


def _client_factory(transport: httpx.MockTransport):
    def factory(*args, **kwargs):
        kwargs.pop("verify", None)
        kwargs["transport"] = transport
        return _RealAsyncClient(**kwargs)
    return factory


# ── extract_service_mentions (pure function, mirrors src/agent's coverage) ──

def test_extract_accepts_fully_qualified_mention():
    log_text = "2026-08-02 calling http://payment-backend.payments.svc.cluster.local:8080/charge"
    found = st.extract_service_mentions(log_text, "payments", {"payment-backend"})
    assert found == {"payment-backend"}


def test_extract_accepts_partially_qualified_mention():
    log_text = "connecting to payment-backend.payments now"
    found = st.extract_service_mentions(log_text, "payments", {"payment-backend"})
    assert found == {"payment-backend"}


def test_extract_rejects_bare_unqualified_mention():
    log_text = "payment-backend restarted due to OOMKilled"
    found = st.extract_service_mentions(log_text, "payments", {"payment-backend"})
    assert found == set()


def test_extract_rejects_wrong_namespace():
    log_text = "calling payment-backend.orders.svc.cluster.local"
    found = st.extract_service_mentions(log_text, "payments", {"payment-backend"})
    assert found == set()


def test_extract_rejects_unknown_service():
    log_text = "calling unknown-svc.payments.svc.cluster.local"
    found = st.extract_service_mentions(log_text, "payments", {"payment-backend"})
    assert found == set()


def test_extract_finds_multiple_distinct_services():
    log_text = (
        "step 1: payment-ui.payments.svc.cluster.local ok\n"
        "step 2: payment-backend.payments ok\n"
    )
    found = st.extract_service_mentions(log_text, "payments", {"payment-ui", "payment-backend"})
    assert found == {"payment-ui", "payment-backend"}


def test_extract_empty_inputs_return_empty_set():
    assert st.extract_service_mentions("", "payments", {"payment-backend"}) == set()
    assert st.extract_service_mentions("payment-backend.payments", "payments", set()) == set()


# ── push_dependency ───────────────────────────────────────────────────────────

@pytest.mark.asyncio
async def test_push_dependency_sends_bearer_token(monkeypatch):
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["auth"] = request.headers.get("authorization")
        seen["body"] = request.content
        return httpx.Response(204)

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    await st.push_dependency("payments", "payment-ui", "payment-backend", "http://backend", "secret-token")

    assert seen["auth"] == "Bearer secret-token"


@pytest.mark.asyncio
async def test_push_dependency_degrades_silently_on_error(monkeypatch):
    def handler(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("connection refused")

    monkeypatch.setattr(httpx, "AsyncClient", _client_factory(httpx.MockTransport(handler)))

    # Should not raise — best-effort, same convention as the original.
    await st.push_dependency("payments", "payment-ui", "payment-backend", "http://backend", "secret-token")
