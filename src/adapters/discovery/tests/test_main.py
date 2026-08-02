"""Tests for main.py's _service_for_pod — new logic (not copied from
src/agent), so it gets its own coverage. Matches a pod to the Service that
selects it via the same label-selector semantics K8s itself uses to build
Service endpoints.
"""

from discovery.main import _service_for_pod


def test_matches_pod_via_selector():
    services = [{"name": "payment-backend", "selector": {"app": "payment-backend"}}]
    assert _service_for_pod({"app": "payment-backend", "pod-template-hash": "abc123"}, services) == "payment-backend"


def test_no_match_returns_none():
    services = [{"name": "payment-backend", "selector": {"app": "payment-backend"}}]
    assert _service_for_pod({"app": "payment-ui"}, services) is None


def test_selector_must_be_fully_satisfied():
    services = [{"name": "payment-backend", "selector": {"app": "payment-backend", "tier": "backend"}}]
    # Missing the "tier" label -> not a match, even though "app" matches.
    assert _service_for_pod({"app": "payment-backend"}, services) is None


def test_empty_selector_never_matches():
    services = [{"name": "manually-managed", "selector": {}}]
    assert _service_for_pod({"app": "anything"}, services) is None


def test_picks_first_matching_service_among_several():
    services = [
        {"name": "payment-ui", "selector": {"app": "payment-ui"}},
        {"name": "payment-backend", "selector": {"app": "payment-backend"}},
    ]
    assert _service_for_pod({"app": "payment-backend"}, services) == "payment-backend"
