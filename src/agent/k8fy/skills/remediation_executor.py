"""RemediationExecutorSkill — handles execute_remediation intent (ADR 0020).

Deterministic dispatch only — zero Claude calls. This is reached exclusively
by the backend after a human has approved a pending remediation proposal
(POST /admin/remediation/{id}/approve). The decision (which action, on what)
was already made and reviewed; this skill's only job is to invoke the
matching action_executor function. It never decides, only executes.
"""

import logging
from typing import Any, Dict

from k8fy.action_executor import restart_deployment, rollback_deployment, scale_deployment
from k8fy.agent import K8fyAgent
from models.response import AgentResponse

logger = logging.getLogger(__name__)


class RemediationExecutorSkill(K8fyAgent):
    """Executes an already-approved remediation action. No LLM call."""

    async def reason(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any] | None = None
    ) -> AgentResponse:
        if context is None:
            context = {}
        action = context.get("action", "")
        namespace = context.get("namespace", "")
        deployment = context.get("deployment") or context.get("service", "")

        if action == "restart_deployment":
            result = await restart_deployment(namespace, deployment)
        elif action == "scale_deployment":
            replicas = context.get("replicas")
            if replicas is None:
                result = {"error": "scale_deployment requires 'replicas' in the approved proposal's action_params"}
            else:
                result = await scale_deployment(namespace, deployment, int(replicas))
        elif action == "rollback_deployment":
            result = await rollback_deployment(namespace, deployment, self.backend_url)
        else:
            result = {
                "error": (
                    f"execute_remediation does not support action '{action}' — only "
                    "restart_deployment/scale_deployment/rollback_deployment run through "
                    "this deterministic path (rotate_cert already has its own on-demand "
                    "renewal flow; human_escalation performs no action)"
                )
            }

        if "error" in result:
            logger.warning("remediation execution failed", extra={"action": action, "namespace": namespace, "deployment": deployment, "error": result["error"]})
            return AgentResponse(answer=result["error"], status="error", confidence=0.0, details=result)
        return AgentResponse(
            answer=f"{action} completed for {namespace}/{deployment}.",
            status="ok",
            confidence=1.0,
            details=result,
        )
