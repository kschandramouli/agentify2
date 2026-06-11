"""RestartTrendSkill — summarises restart-count time series for a service."""

from k8fy.agent import K8fyAgent
from k8fy.prompts import RESTART_TREND_PROMPT
from k8fy.tools import TOOLS

_TOOLS = [t for t in TOOLS if t["name"] in {"get_metrics_history"}]


class RestartTrendSkill(K8fyAgent):
    """Reads and interprets a restart-count time series for one service.

    Single-tool, single-model path (no advisor): trend analysis is a
    straightforward arithmetic task over a time series, not multi-signal reasoning.
    """

    def __init__(self) -> None:
        super().__init__(system_prompt=RESTART_TREND_PROMPT, tools=_TOOLS)
