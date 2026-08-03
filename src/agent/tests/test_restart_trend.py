"""Tests for RestartTrendSkill's prefetch (no prior coverage existed) —
including the fleet-cluster scoping added by ROADMAP P16 / ADR 0023/0024:
every cluster resolve_service_clusters finds for the service being checked
gets a cluster-scoped get_metrics_history prefetch, alongside the existing
unscoped call. No live equivalent exists for restart metrics, so this is the
ingested-data path only.
"""

import pytest

from k8fy.skills import restart_trend as restart_trend_module
from k8fy.skills.restart_trend import RestartTrendSkill


@pytest.mark.asyncio
async def test_prefetch_always_fetches_unscoped_metrics_history(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        return []

    monkeypatch.setattr(RestartTrendSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(restart_trend_module, "resolve_service_clusters", fake_resolve)

    skill = RestartTrendSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments", "service_name": "payment-api"})

    assert prefetched.get("metrics_history") == {"stub": True}
    assert ("get_metrics_history", {"namespace": "payments", "order": "asc", "service_name": "payment-api"}) in calls


@pytest.mark.asyncio
async def test_prefetch_fans_out_per_resolved_cluster(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        assert namespace == "payments"
        assert service == "payment-api"
        return ["cluster-42", "cluster-99"]

    monkeypatch.setattr(RestartTrendSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(restart_trend_module, "resolve_service_clusters", fake_resolve)

    skill = RestartTrendSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments", "service_name": "payment-api"})

    assert prefetched.get("metrics_history.cluster-42") == {"stub": True}
    assert prefetched.get("metrics_history.cluster-99") == {"stub": True}

    scoped_calls = [args for name, args in calls if name == "get_metrics_history" and "cluster_id" in args]
    assert {"namespace": "payments", "order": "asc", "service_name": "payment-api", "cluster_id": "cluster-42"} in scoped_calls
    assert {"namespace": "payments", "order": "asc", "service_name": "payment-api", "cluster_id": "cluster-99"} in scoped_calls


@pytest.mark.asyncio
async def test_prefetch_skips_fanout_without_service_name(monkeypatch):
    resolve_called = False

    async def fake_fetch(self, tool_name, args):
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        nonlocal resolve_called
        resolve_called = True
        return ["cluster-42"]

    monkeypatch.setattr(RestartTrendSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(restart_trend_module, "resolve_service_clusters", fake_resolve)

    skill = RestartTrendSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments"})

    assert resolve_called is False
    assert not any(key.startswith("metrics_history.") for key in prefetched)
