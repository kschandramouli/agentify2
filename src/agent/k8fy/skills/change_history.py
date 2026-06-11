"""ChangeHistorySkill — lists recent deploy/rollout events for a service."""

from k8fy.agent import K8fyAgent
from k8fy.prompts import CHANGE_HISTORY_PROMPT
from k8fy.tools import TOOLS

_TOOLS = [t for t in TOOLS if t["name"] in {"get_change_history"}]


class ChangeHistorySkill(K8fyAgent):
    """Lists and correlates recent deployment events for one service.

    Single-tool, single-model path (no advisor): change history queries are
    straightforward data retrieval + formatting, not multi-signal reasoning.
    """

    def __init__(self) -> None:
        super().__init__(system_prompt=CHANGE_HISTORY_PROMPT, tools=_TOOLS)
