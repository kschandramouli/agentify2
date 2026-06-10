"""DiagnoseSkill — focused skill for diagnose intent (spec 010).

Cert data arrives in the pre-fetched payload (fan-out includes cert pods), so
get_certificates is excluded from the tool set — the skill reads cert context
from the initial data without a redundant tool call.
"""

from k8fy.agent import ADVISOR_MODEL, EXECUTOR_MODEL, K8fyAgent
from k8fy.prompts import DIAGNOSE_PROMPT
from k8fy.tools import TOOLS

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


class DiagnoseSkill(K8fyAgent):
    """Failure-mode + causal correlation expert — 6 tools, crash-focused prompt.

    Uses the advisor/executor strategy: Opus plans the diagnostic sequence,
    Sonnet executes the tool calls cheaply.
    """

    def __init__(self) -> None:
        super().__init__(
            system_prompt=DIAGNOSE_PROMPT,
            tools=_DIAGNOSE_TOOLS,
            advisor_model=ADVISOR_MODEL,
            executor_model=EXECUTOR_MODEL,
        )
