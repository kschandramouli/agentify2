"""Tests for K8fyAgent._structure_chat_answer() — the second, schema-constrained
call that restructures reason_chat()'s free-form answer into sections.

The load-bearing assertion is the namespace override: the model has been
observed guessing "default" for a recommended_action's namespace instead of
using the conversation's real one — since RBAC (agent-live-diagnostics) only
grants access within the actual namespace, a wrong guess fails with 403 at
click-time. The fix never trusts the model for this field; the conversation's
own context always wins.
"""

import json
from types import SimpleNamespace

import pytest

from k8fy.agent import K8fyAgent


def _fake_response(payload: dict, usage=None):
    return SimpleNamespace(
        content=[SimpleNamespace(type="text", text=json.dumps(payload))],
        usage=usage or SimpleNamespace(
            input_tokens=10, output_tokens=5,
            cache_creation_input_tokens=0, cache_read_input_tokens=0,
        ),
    )


def _base_payload(**overrides):
    payload = {
        "status": "healthy",
        "severity": "info",
        "confidence": 0.9,
        "incident_summary": "payment-worker is healthy.",
        "timeline": ["2026-07-24: stable"],
        "findings": ["0 restarts"],
        "likely_cause": None,
        "recommendations": ["No action needed"],
        "recommended_actions": [],
    }
    payload.update(overrides)
    return payload


@pytest.mark.asyncio
async def test_structure_chat_answer_overrides_hallucinated_namespace(monkeypatch):
    agent = K8fyAgent()
    payload = _base_payload(recommended_actions=[{
        "label": "Verify live pod status for payment-worker",
        "tool": "live_list_pods",
        "arguments": {"namespace": "default", "pod": None, "container": None, "tail_lines": None, "previous": None},
    }])

    async def fake_create(**kwargs):
        return _fake_response(payload)

    monkeypatch.setattr(agent.client.messages, "create", fake_create)

    details, usage = await agent._structure_chat_answer(
        "payment-worker is healthy.", {"namespace": "payments", "service": "payment-worker"},
    )

    assert details["recommended_actions"] == [{
        "label": "Verify live pod status for payment-worker",
        "tool": "live_list_pods",
        "arguments": {"namespace": "payments"},
    }]
    assert usage == (10, 5, 0, 0)


@pytest.mark.asyncio
async def test_structure_chat_answer_keeps_other_arguments(monkeypatch):
    agent = K8fyAgent()
    payload = _base_payload(recommended_actions=[{
        "label": "Check payment-worker logs",
        "tool": "live_get_pod_logs",
        "arguments": {"namespace": "default", "pod": "payment-worker-abc", "container": None, "tail_lines": 100, "previous": None},
    }])

    async def fake_create(**kwargs):
        return _fake_response(payload)

    monkeypatch.setattr(agent.client.messages, "create", fake_create)

    details, _ = await agent._structure_chat_answer(
        "answer", {"namespace": "payments", "service": "payment-worker"},
    )

    assert details["recommended_actions"][0]["arguments"] == {
        "namespace": "payments", "pod": "payment-worker-abc", "tail_lines": 100,
    }


@pytest.mark.asyncio
async def test_structure_chat_answer_no_context_namespace_leaves_model_value(monkeypatch):
    agent = K8fyAgent()
    payload = _base_payload(recommended_actions=[{
        "label": "Verify live pod status",
        "tool": "live_list_pods",
        "arguments": {"namespace": "default", "pod": None, "container": None, "tail_lines": None, "previous": None},
    }])

    async def fake_create(**kwargs):
        return _fake_response(payload)

    monkeypatch.setattr(agent.client.messages, "create", fake_create)

    details, _ = await agent._structure_chat_answer("answer", {})

    # No namespace known from context — can't override with an empty value,
    # so the model's (possibly wrong) guess passes through unchanged.
    assert details["recommended_actions"][0]["arguments"]["namespace"] == "default"


@pytest.mark.asyncio
async def test_structure_chat_answer_empty_text_short_circuits(monkeypatch):
    agent = K8fyAgent()

    async def fake_create(**kwargs):
        raise AssertionError("should not call the API for empty answer text")

    monkeypatch.setattr(agent.client.messages, "create", fake_create)

    details, usage = await agent._structure_chat_answer("   ", {"namespace": "payments"})
    assert details == {}
    assert usage == (0, 0, 0, 0)


@pytest.mark.asyncio
async def test_structure_chat_answer_degrades_on_api_error(monkeypatch):
    agent = K8fyAgent()

    async def fake_create(**kwargs):
        raise RuntimeError("API unavailable")

    monkeypatch.setattr(agent.client.messages, "create", fake_create)

    details, usage = await agent._structure_chat_answer("some answer", {"namespace": "payments"})
    assert details == {}
    assert usage == (0, 0, 0, 0)
