"""Configuration for agentify-discovery, loaded from environment variables.

No CLUSTER_ID/TENANT_ID here, deliberately (a correction to ADR 0022
Decision #6's original wording, written up in the ADR amendment): the Hub's
resolveTenantContext (src/backend/internal/api/handlers.go) already derives
both from which Integration row COLLECTOR_TOKEN matches. Injecting a second,
separate cluster identity here would be redundant and could drift from the
Hub's own record.
"""

import os
from dataclasses import dataclass, field
from typing import List


@dataclass
class Config:
    backend_url: str
    collector_token: str
    scan_interval_seconds: int
    max_pods_per_namespace: int
    log_tail_lines: int
    namespace_exclude: List[str] = field(default_factory=list)
    health_port: int = 8300


def load_from_env() -> Config:
    return Config(
        backend_url=os.getenv("BACKEND_URL", "http://localhost:8080"),
        collector_token=os.getenv("COLLECTOR_TOKEN", ""),
        scan_interval_seconds=_int_env("SCAN_INTERVAL_SECONDS", 60),
        max_pods_per_namespace=_int_env("MAX_PODS_PER_NAMESPACE", 5),
        log_tail_lines=_int_env("LOG_TAIL_LINES", 200),
        namespace_exclude=_list_env(
            "NAMESPACE_EXCLUDE",
            # Union of the two prior defaults (this package's own + the
            # retired k8fy adapter's broader _SYSTEM_NAMESPACES, ADR 0027).
            "kube-system,kube-public,kube-node-lease,cert-manager,monitoring,ingress-nginx",
        ),
        health_port=_int_env("HEALTH_PORT", 8300),
    )


def _int_env(key: str, default: int) -> int:
    value = os.getenv(key)
    if not value:
        return default
    try:
        return int(value)
    except ValueError:
        return default


def _list_env(key: str, default: str) -> List[str]:
    value = os.getenv(key, default)
    return [v.strip() for v in value.split(",") if v.strip()]
