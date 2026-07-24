"""k8s_client.py — shared in-cluster Kubernetes API authentication.

Both `action_executor.py` (write actions, ADR 0020) and `live_diagnostics.py`
(read-only live queries) authenticate the same way: read the pod's mounted
service-account token and call `https://kubernetes.default.svc` directly over
HTTPS with a Bearer header. Factored out here so there is exactly one place
that reads the token, rather than two copies drifting apart.
"""

from typing import Dict

_SA_TOKEN_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/token"
K8S_API = "https://kubernetes.default.svc"


def k8s_headers(content_type: str = "application/json") -> Dict[str, str]:
    try:
        with open(_SA_TOKEN_PATH) as f:
            token = f.read()
    except OSError:
        return {}
    return {"Authorization": f"Bearer {token}", "Content-Type": content_type}
