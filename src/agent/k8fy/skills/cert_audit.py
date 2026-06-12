"""CertAuditSkill — Pattern A skill for cert_check intent (spec 010).

Pattern A: always pre-fetches the full certificate list for the namespace
before calling Claude. The cert list is the only data source this skill needs,
its parameters are fully known from context alone, and it never varies by what
the initial data contains — making it the cleanest Pattern A candidate.

Pre-fetch sequence:
  1. get_certificates(namespace)  — always, unconditionally
"""

import logging
from typing import Any, Dict

from k8fy.agent import K8fyAgent
from k8fy.prompt_manager import get_prompt
from k8fy.prompts import CERT_AUDIT_PROMPT
from k8fy.tools import TOOLS
from models.response import AgentResponse

_CERT_TOOLS = [t for t in TOOLS if t["name"] == "get_certificates"]

logger = logging.getLogger(__name__)


class CertAuditSkill(K8fyAgent):
    """PKI/TLS lifecycle expert — Pattern A: pre-fetch certs + single Claude call."""

    def __init__(self) -> None:
        super().__init__(
            system_prompt=get_prompt("k8fy/cert-audit", CERT_AUDIT_PROMPT),
            tools=_CERT_TOOLS,
        )

    async def reason(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any] | None = None
    ) -> AgentResponse:
        if context is None:
            context = {}
        namespace = context.get("namespace", "default")
        certs = await self._fetch("get_certificates", {"namespace": namespace})
        return await self._reason_pattern_a(
            intent, data, context, prefetched={"certificates": certs}
        )
