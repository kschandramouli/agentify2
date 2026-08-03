"""Tests for HealthSkill's prefetch (no prior coverage existed) — including
the fleet-cluster scoping added by ROADMAP P16 / ADR 0023/0024: every
cluster resolve_service_clusters finds for the service being checked gets a
cluster-scoped get_service_health and a live_list_pods snapshot, mirroring
DiagnoseSkill's pattern.
"""

import pytest

from k8fy.skills import health_check as health_check_module
from k8fy.skills.health_check import HealthSkill


@pytest.mark.asyncio
async def test_prefetch_fetches_service_health_when_service_name_present(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        return []

    monkeypatch.setattr(HealthSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(health_check_module, "resolve_service_clusters", fake_resolve)

    skill = HealthSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments", "service_name": "payment-api"})

    assert prefetched.get("service_health") == {"stub": True}
    assert ("get_service_health", {"service_name": "payment-api", "namespace": "payments"}) in calls


@pytest.mark.asyncio
async def test_prefetch_fans_out_to_every_resolved_cluster(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        assert namespace == "payments"
        assert service == "payment-api"
        return ["cluster-42", "cluster-99"]

    monkeypatch.setattr(HealthSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(health_check_module, "resolve_service_clusters", fake_resolve)

    skill = HealthSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments", "service_name": "payment-api"})

    assert prefetched.get("service_health.cluster-42") == {"stub": True}
    assert prefetched.get("service_health.cluster-99") == {"stub": True}
    assert prefetched.get("live_pods.cluster-42") == {"stub": True}
    assert prefetched.get("live_pods.cluster-99") == {"stub": True}

    health_calls = [args for name, args in calls if name == "get_service_health"]
    assert {"service_name": "payment-api", "namespace": "payments", "cluster_id": "cluster-42"} in health_calls
    assert {"service_name": "payment-api", "namespace": "payments", "cluster_id": "cluster-99"} in health_calls


@pytest.mark.asyncio
async def test_prefetch_skips_cluster_fanout_when_none_resolve(monkeypatch):
    async def fake_fetch(self, tool_name, args):
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        return []

    monkeypatch.setattr(HealthSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(health_check_module, "resolve_service_clusters", fake_resolve)

    skill = HealthSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments", "service_name": "payment-api"})

    assert not any(key.startswith("live_pods.") or "." in key and key.startswith("service_health") for key in prefetched)


@pytest.mark.asyncio
async def test_prefetch_fetches_events_for_degraded_pods(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        return []

    monkeypatch.setattr(HealthSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(health_check_module, "resolve_service_clusters", fake_resolve)

    skill = HealthSkill()
    data = {"payment-api-abc": {"restarts": 3, "ready": False}, "payment-api-xyz": {"restarts": 0, "ready": True}}
    prefetched = await skill._prefetch(data, {"namespace": "payments"})

    assert "events.payment-api-abc" in prefetched
    assert "events.payment-api-xyz" not in prefetched
