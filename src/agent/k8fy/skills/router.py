"""SkillRouter — dispatches each intent to the appropriate skill sub-agent (spec 010).

The intent is pre-classified by Go's inferIntent() before the /reason request
arrives, so this is a pure O(1) dispatch table with no classification cost.
"""

import logging
from typing import Any, Dict, Optional

from k8fy.agent import K8fyAgent, get_k8fy_agent
from k8fy.skills.cert_audit import CertAuditSkill
from k8fy.skills.diagnose import DiagnoseSkill
from k8fy.skills.health_check import HealthSkill
from models.response import AgentResponse

logger = logging.getLogger(__name__)


class SkillRouter:
    """Routes each intent to the narrowest skill that can handle it."""

    def __init__(self) -> None:
        self._skills: Dict[str, K8fyAgent] = {
            "health_check": HealthSkill(),
            "cert_check": CertAuditSkill(),
            "diagnose": DiagnoseSkill(),
        }
        # Fallback: full K8fyAgent for general_query, metrics_query, or anything new.
        self._fallback: Optional[K8fyAgent] = None

    async def dispatch(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any]
    ) -> AgentResponse:
        skill = self._skills.get(intent)
        if skill is None:
            if self._fallback is None:
                self._fallback = get_k8fy_agent()
            skill = self._fallback
            logger.debug("skill router: no skill for intent %r, using fallback", intent)
        else:
            logger.debug("skill router: intent %r → %s", intent, type(skill).__name__)
        return await skill.reason(intent, data, context)


_router: Optional[SkillRouter] = None


def get_skill_router() -> SkillRouter:
    """Return the process-wide SkillRouter singleton."""
    global _router
    if _router is None:
        _router = SkillRouter()
    return _router
