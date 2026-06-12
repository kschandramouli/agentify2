"""ChangeHistorySkill — Pattern A: pre-fetch change events, one Claude call (spec 010).

Pre-fetch sequence:
  1. get_change_history(namespace, service_name) — always, unconditionally.

Change events are the single data source for this intent; their parameters are
fully known from context, so pre-fetching before the Claude call is safe and
eliminates the agentic tool-call round-trip entirely.

Cost: 1 backend fetch + exactly 1 Claude call (predictable).
"""

import logging
from typing import Any, Dict

from k8fy.agent import K8fyAgent
from k8fy.prompts import CHANGE_HISTORY_PROMPT
from k8fy.tools import TOOLS
from models.response import AgentResponse

_TOOLS = [t for t in TOOLS if t["name"] in {"get_change_history"}]

logger = logging.getLogger(__name__)


class ChangeHistorySkill(K8fyAgent):
    """Deploy/rollout timeline expert — Pattern A: unconditional pre-fetch + single Claude call."""

    def __init__(self) -> None:
        super().__init__(system_prompt=CHANGE_HISTORY_PROMPT, tools=_TOOLS)

    async def reason(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any] | None = None
    ) -> AgentResponse:
        if context is None:
            context = {}
        prefetched = await self._prefetch(data, context)
        return await self._reason_pattern_a(intent, data, context, prefetched)

    async def _prefetch(
        self, data: Dict[str, Any], context: Dict[str, Any]
    ) -> Dict[str, Any]:
        """Fetch change history unconditionally — it is always the sole data source."""
        namespace = context.get("namespace", "default")
        service_name = (
            context.get("service_name")
            or context.get("deployment")
            or context.get("service")
        )
        args: Dict[str, Any] = {"namespace": namespace}
        if service_name:
            args["service_name"] = service_name
        try:
            result = await self._fetch("get_change_history", args)
            return {"change_history": result}
        except Exception as exc:
            logger.warning("change_history prefetch failed: %s", exc)
            return {}
