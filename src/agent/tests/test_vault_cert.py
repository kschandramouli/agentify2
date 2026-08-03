"""Tests for VaultCertSkill's prefetch (no prior coverage existed) —
including the fleet-cluster scoping added by ROADMAP P16 / ADR 0023/0024.
Only the K8s-cert half (get_certificates) is cluster-scoped; Vault has no
per-cluster routing concept (single VAULT_ADDR), so get_vault_cert_status
must never receive a cluster_id.
"""

import pytest

from k8fy.skills import vault_cert as vault_cert_module
from k8fy.skills.vault_cert import VaultCertSkill


@pytest.mark.asyncio
async def test_prefetch_always_fetches_vault_and_k8s_certs_unscoped(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        return []

    monkeypatch.setattr(VaultCertSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(vault_cert_module, "resolve_service_clusters", fake_resolve)

    skill = VaultCertSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments"})

    assert prefetched.get("vault_cert") == {"stub": True}
    assert prefetched.get("k8s_certs") == {"stub": True}
    tool_names = [name for name, _ in calls]
    assert "get_vault_cert_status" in tool_names
    assert "get_certificates" in tool_names


@pytest.mark.asyncio
async def test_prefetch_fans_out_k8s_certs_only_per_resolved_cluster(monkeypatch):
    calls = []

    async def fake_fetch(self, tool_name, args):
        calls.append((tool_name, dict(args)))
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        assert namespace == "payments"
        assert service == "payment-api"
        return ["cluster-42", "cluster-99"]

    monkeypatch.setattr(VaultCertSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(vault_cert_module, "resolve_service_clusters", fake_resolve)

    skill = VaultCertSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments", "service_name": "payment-api"})

    assert prefetched.get("k8s_certs.cluster-42") == {"stub": True}
    assert prefetched.get("k8s_certs.cluster-99") == {"stub": True}

    # get_vault_cert_status must NEVER receive a cluster_id -- no per-cluster
    # Vault routing concept exists.
    vault_calls = [args for name, args in calls if name == "get_vault_cert_status"]
    assert len(vault_calls) == 1
    assert "cluster_id" not in vault_calls[0]

    scoped_cert_calls = [args for name, args in calls if name == "get_certificates" and "cluster_id" in args]
    assert {"namespace": "payments", "cluster_id": "cluster-42"} in scoped_cert_calls
    assert {"namespace": "payments", "cluster_id": "cluster-99"} in scoped_cert_calls


@pytest.mark.asyncio
async def test_prefetch_skips_fanout_without_service_name(monkeypatch):
    resolve_called = False

    async def fake_fetch(self, tool_name, args):
        return {"stub": True}

    async def fake_resolve(namespace, service, backend_url):
        nonlocal resolve_called
        resolve_called = True
        return ["cluster-42"]

    monkeypatch.setattr(VaultCertSkill, "_fetch", fake_fetch)
    monkeypatch.setattr(vault_cert_module, "resolve_service_clusters", fake_resolve)

    skill = VaultCertSkill()
    prefetched = await skill._prefetch({}, {"namespace": "payments"})

    assert resolve_called is False
    assert not any(key.startswith("k8s_certs.") for key in prefetched)
