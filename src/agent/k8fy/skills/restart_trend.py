"""RestartTrendSkill — Pattern A: pre-fetch restart metrics, one Claude call (spec 010).

Pre-fetch sequence:
  1. get_metrics_history(namespace, service_name, order=asc) — always, unconditionally.

Restart time-series is the single data source for this intent; its parameters are
fully known from context, so pre-fetching before the Claude call is safe and
eliminates the agentic tool-call round-trip entirely.

Cost: 1 backend fetch + exactly 1 Claude call (predictable).
"""

import logging
from typing import Any, Dict

from k8fy.agent import K8fyAgent
from k8fy.prompts import RESTART_TREND_PROMPT
from k8fy.tools import TOOLS
from models.response import AgentResponse

_TOOLS = [t for t in TOOLS if t["name"] in {"get_metrics_history"}]

logger = logging.getLogger(__name__)


class RestartTrendSkill(K8fyAgent):
    """Restart-trend analyst — Pattern A: unconditional pre-fetch + single Claude call."""

    def __init__(self) -> None:
        super().__init__(system_prompt=RESTART_TREND_PROMPT, tools=_TOOLS)

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
        """Fetch restart time-series unconditionally — it is always the sole data source."""
        namespace = context.get("namespace", "default")
        service_name = context.get("service_name") or context.get("service")
        args: Dict[str, Any] = {"namespace": namespace, "order": "asc"}
        if service_name:
            args["service_name"] = service_name
        try:
            result = await self._fetch("get_metrics_history", args)
            return {"metrics_history": result}
        except Exception as exc:
            logger.warning("metrics_history prefetch failed: %s", exc)
            return {}
