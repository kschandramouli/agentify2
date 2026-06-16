"""VaultCertSkill — Pattern A skill for vault_cert intent.

Pre-fetches:
  1. Vault PKI cert status for the specified role (days remaining, expiry)
  2. K8s TLS cert status for the same namespace (from the existing cert tool)

Then makes a single Claude call to:
  - Assess whether rotation is needed
  - Correlate Vault cert state with K8s deployment health
  - Decide whether to call rotate_vault_cert

This is the agentify Vault integration: it monitors certs managed by Vault PKI
and can trigger rotation through the Vault API — mimicking a client setup where
TLS certs are issued and rotated via HashiCorp Vault rather than cert-manager.
"""

import asyncio
import logging
from typing import Any, Dict

from k8fy.agent import K8fyAgent
from k8fy.prompt_manager import get_prompt
from k8fy.prompts import VAULT_CERT_PROMPT
from k8fy.tools import TOOLS

logger = logging.getLogger(__name__)

# Only expose the tools this skill needs: Vault + existing K8s cert + health.
_VAULT_TOOLS = [
    t for t in TOOLS
    if t["name"] in {
        "get_vault_cert_status",
        "rotate_vault_cert",
        "get_certificates",
        "get_service_health",
    }
]


class VaultCertSkill(K8fyAgent):
    """Vault PKI certificate monitoring and rotation — Pattern A."""

    def __init__(self) -> None:
        super().__init__(
            system_prompt=get_prompt("k8fy/vault-cert", VAULT_CERT_PROMPT),
            tools=_VAULT_TOOLS,
        )

    async def reason(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any] | None = None
    ) -> Any:
        if context is None:
            context = {}
        prefetched = await self._prefetch(data, context)
        return await self._reason_pattern_a(intent, data, context, prefetched)

    async def _prefetch(
        self, data: Dict[str, Any], context: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Fetch Vault cert status and K8s cert status in parallel."""
        namespace = context.get("namespace", "payments")
        pki_role  = context.get("pki_role", data.get("pki_role", "payment-service"))
        kv_path   = context.get("kv_path",  data.get("kv_path",  "secret/data/payments/tls"))

        tasks = {
            "vault_cert":  self._fetch("get_vault_cert_status", {
                "pki_role": pki_role,
                "kv_path":  kv_path,
            }),
            "k8s_certs":   self._fetch("get_certificates", {"namespace": namespace}),
        }

        results = await asyncio.gather(*tasks.values(), return_exceptions=True)
        prefetched: Dict[str, Any] = {}
        for key, result in zip(tasks.keys(), results):
            if isinstance(result, Exception):
                logger.warning("vault_cert prefetch failed for %s: %s", key, result)
            else:
                prefetched[key] = result

        return prefetched
