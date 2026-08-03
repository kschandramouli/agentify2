"""Tests for CertAuditSkill's _prefetch (no prior coverage existed — the
skill previously inlined a single get_certificates call in reason()) —
including the fleet-cluster scoping added by ROADMAP P16 / ADR 0023/0024:
every cluster resolve_service_clusters finds for the service being checked
gets a live_get_certificates prefetch, alongside the existing unscoped
get_certificates call.
"""

import pytest

from k8fy.skills import cert_audit as cert_audit_module
from k8fy.skills.cert_audit import CertAuditSkill


@pytest.mark.asyncio
async def test_prefetch_always_fetches_unscoped_certificates(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        return []

    monkeypatch.setattr(CertAuditSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(cert_audit_module, "resolve_service_clusters", fake_resolve)

    skill = CertAuditSkill()
    prefetched = await skill._prefetch({"namespace": "payments"})

    assert prefetched.get("certificates") == {"stub": True}
    assert ("get_certificates", {"namespace": "payments"}) in calls


@pytest.mark.asyncio
async def test_prefetch_fans_out_live_certificates_per_resolved_cluster(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        assert namespace == "payments"
        assert service == "payment-api"
        return ["cluster-42", "cluster-99"]

    monkeypatch.setattr(CertAuditSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(cert_audit_module, "resolve_service_clusters", fake_resolve)

    skill = CertAuditSkill()
    prefetched = await skill._prefetch({"namespace": "payments", "service_name": "payment-api"})

    assert prefetched.get("live_certificates.cluster-42") == {"stub": True}
    assert prefetched.get("live_certificates.cluster-99") == {"stub": True}

    live_calls = [args for name, args in calls if name == "live_get_certificates"]
    assert {"namespace": "payments", "cluster_id": "cluster-42"} in live_calls
    assert {"namespace": "payments", "cluster_id": "cluster-99"} in live_calls


@pytest.mark.asyncio
async def test_prefetch_skips_live_fanout_without_service_name(monkeypatch):
    resolve_called = False

    async def fake_fetch(self, tool_name, args):
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        nonlocal resolve_called
        resolve_called = True
        return ["cluster-42"]

    monkeypatch.setattr(CertAuditSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(cert_audit_module, "resolve_service_clusters", fake_resolve)

    skill = CertAuditSkill()
    prefetched = await skill._prefetch({"namespace": "payments"})

    assert resolve_called is False
    assert not any(key.startswith("live_certificates.") for key in prefetched)
