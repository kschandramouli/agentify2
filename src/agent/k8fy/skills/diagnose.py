"""DiagnoseSkill — Pattern A: parallel multi-signal pre-fetch, one Opus call (spec 010).

Replaces the advisor/executor agentic loop with a deterministic pre-fetch of every
signal the diagnosis needs, followed by a single Opus call with all data assembled.

Pre-fetch sequence (all parallel via asyncio.gather):
  1. get_service_health(service_name, namespace)        — service-level status
  2. get_pod_events(pod_id, namespace)                  — for every pod in initial data
  3. get_metrics_history(namespace, service_name, asc)  — restart time-series
  4. get_change_history(namespace, service_name)        — deploy/rollout correlation
  5. get_pod_logs(pod_id, namespace, previous=True)     — only for crashing pods
     (restarts >= _CRASH_RESTART_THRESHOLD or phase in _CRASH_PHASES)

Cost: (4 + crashing-pod-count) parallel backend fetches + exactly 1 Opus call.
Previously: advisor/executor loop with 2–7 tool iterations (unpredictable cost).
"""

import asyncio
import logging
from typing import Any, Dict, List

from k8fy.agent import ADVISOR_MODEL, K8fyAgent
from k8fy.prompts import DIAGNOSE_PROMPT
from k8fy.tools import TOOLS
from models.response import AgentResponse

_DIAGNOSE_TOOLS = [
    t for t in TOOLS
    if t["name"] in {
        "get_service_health",
        "query_pod",
        "get_pod_events",
        "get_pod_logs",
        "get_metrics_history",
        "get_change_history",
    }
]

logger = logging.getLogger(__name__)

_CRASH_RESTART_THRESHOLD = 3
_CRASH_PHASES = {"Failed", "Unknown", "CrashLoopBackOff"}


class DiagnoseSkill(K8fyAgent):
    """Failure-mode + causal correlation expert — Pattern A: parallel pre-fetch + one Opus call."""

    def __init__(self) -> None:
        super().__init__(system_prompt=DIAGNOSE_PROMPT, tools=_DIAGNOSE_TOOLS)
        # Diagnosis warrants the most capable model; override the default.
        self.model = ADVISOR_MODEL  # claude-opus-4-8

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
        """Fire all predictable diagnostic tool calls in parallel before the Opus call."""
        namespace = context.get("namespace", "default")
        service_name = context.get("service_name") or context.get("service")
        tasks: Dict[str, Any] = {}

        # 1. Service-level health summary
        if service_name:
            tasks["service_health"] = self._fetch(
                "get_service_health",
                {"service_name": service_name, "namespace": namespace},
            )

        # 2. Pod events — for every pod present in the initial data
        for pod_id in _all_pod_ids(data):
            tasks[f"events.{pod_id}"] = self._fetch(
                "get_pod_events",
                {"pod_id": pod_id, "namespace": namespace},
            )

        # 3. Restart time-series — always, asc so Claude can read the trend
        metrics_args: Dict[str, Any] = {"namespace": namespace, "order": "asc"}
        if service_name:
            metrics_args["service_name"] = service_name
        tasks["metrics_history"] = self._fetch("get_metrics_history", metrics_args)

        # 4. Deploy/rollout history — always, for symptom-onset correlation
        change_args: Dict[str, Any] = {"namespace": namespace}
        if service_name:
            change_args["service_name"] = service_name
        tasks["change_history"] = self._fetch("get_change_history", change_args)

        # 5. Crash logs — only for pods that look like they are crashing;
        #    fetching logs for healthy pods wastes tokens and latency.
        for pod_id in _crashing_pod_ids(data):
            tasks[f"logs.{pod_id}"] = self._fetch(
                "get_pod_logs",
                {"pod_id": pod_id, "namespace": namespace, "previous": True},
            )

        if not tasks:
            return {}

        results = await asyncio.gather(*tasks.values(), return_exceptions=True)
        prefetched: Dict[str, Any] = {}
        for key, result in zip(tasks.keys(), results):
            if isinstance(result, Exception):
                logger.warning("diagnose prefetch failed for %s: %s", key, result)
            else:
                prefetched[key] = result
        return prefetched


def _all_pod_ids(data: Dict[str, Any]) -> List[str]:
    """Return keys whose values look like pod-state dicts (have phase or ready)."""
    return [
        k for k, v in data.items()
        if isinstance(v, dict) and ("phase" in v or "ready" in v)
    ]


def _crashing_pod_ids(data: Dict[str, Any]) -> List[str]:
    """Return pods that appear to be crashing — logs are worth fetching for these."""
    ids = []
    for key, val in data.items():
        if not isinstance(val, dict):
            continue
        if (
            val.get("restarts", 0) >= _CRASH_RESTART_THRESHOLD
            or val.get("phase", "") in _CRASH_PHASES
        ):
            ids.append(key)
    return ids
