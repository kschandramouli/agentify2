"""K8fy agent: Claude-powered Kubernetes operations reasoning."""

import json
import logging
from typing import Any, Dict, List, Optional

from anthropic import AsyncAnthropic
from pydantic import ValidationError

import metrics
from config.claude_client import get_claude_client
from config.settings import get_settings
from k8fy.prompts import SYSTEM_PROMPT
from k8fy.tools import TOOLS, process_tool_call
from models.response import AgentResponse, ReasoningOutput, ToolCall

logger = logging.getLogger(__name__)
settings = get_settings()

# JSON schema the model's final answer is constrained to (output_config.format).
# Mirrors models.ReasoningOutput. Structured outputs require additionalProperties
# to be false and don't support numeric min/max, so confidence is a bare number
# (the system prompt asks for 0.0–1.0; we clamp/normalize on the way out).
REASONING_SCHEMA: Dict[str, Any] = {
    "type": "object",
    "properties": {
        "answer": {"type": "string", "description": "Concise operator-facing answer."},
        "status": {
            "type": "string",
            "enum": ["healthy", "degraded", "unhealthy", "unknown", "not_applicable"],
        },
        "confidence": {"type": "number", "description": "0.0–1.0; lower if data is incomplete."},
        "recommendations": {"type": "array", "items": {"type": "string"}, "description": "Prioritized operator actions."},
        # Correlation fields (spec 005); empty/null for single-signal answers.
        "findings": {
            "type": "array",
            "items": {"type": "string"},
            "description": "One short bullet per signal considered (health, cert, …). Empty if not diagnosing.",
        },
        "likely_cause": {
            "type": ["string", "null"],
            "description": "Best-supported hypothesis for a diagnosis; null when signals are insufficient or N/A.",
        },
        "severity": {
            "type": "string",
            "enum": ["info", "warning", "critical"],
        },
    },
    "required": ["answer", "status", "confidence", "recommendations", "findings", "likely_cause", "severity"],
    "additionalProperties": False,
}


class K8fyAgent:
    """K8fy agent for Kubernetes operations reasoning."""

    def __init__(self):
        self.client: AsyncAnthropic = get_claude_client()
        self.model = settings.claude_model
        self.max_tokens = settings.claude_max_tokens
        self.effort = settings.claude_effort
        self.backend_url = settings.backend_url
        self.max_iterations = settings.agent_max_tool_iterations

    async def reason(
        self, intent: str, data: Dict[str, Any], context: Optional[Dict[str, Any]] = None
    ) -> AgentResponse:
        """Reason about K8s operations given data and intent.

        Runs an agentic loop: Claude may call tools (which fetch more data from
        the backend) until it produces a final, schema-constrained answer.
        """
        if context is None:
            context = {}

        # System prompt + tools are the stable cache prefix; cache_control caches
        # them together (tools render before system). Note: the prompt is small,
        # so on Opus 4.8 (4096-token cache minimum) it may not actually cache
        # until it grows — the markers are correct and cost nothing meanwhile.
        system = [{"type": "text", "text": SYSTEM_PROMPT, "cache_control": {"type": "ephemeral"}}]
        messages: List[Dict[str, Any]] = [
            {"role": "user", "content": self._build_user_message(intent, data, context)}
        ]
        tool_calls_made: List[ToolCall] = []
        iterations = 0

        try:
            for _ in range(self.max_iterations):
                iterations += 1
                response = await self.client.messages.create(
                    model=self.model,
                    max_tokens=self.max_tokens,
                    system=system,
                    thinking={"type": "adaptive"},
                    output_config={"effort": self.effort, "format": {"type": "json_schema", "schema": REASONING_SCHEMA}},
                    tools=TOOLS,
                    messages=messages,
                )
                # Record token usage/cost for every model call (incl. tool-loop turns).
                metrics.record_usage(self.model, getattr(response, "usage", None))

                if response.stop_reason == "tool_use":
                    # Preserve the full assistant turn (incl. thinking blocks) before
                    # appending tool results, then run each requested tool.
                    messages.append({"role": "assistant", "content": response.content})
                    tool_results = []
                    for block in response.content:
                        if block.type == "tool_use":
                            tool_calls_made.append(ToolCall(name=block.name, arguments=block.input))
                            result = await process_tool_call(block.name, block.input, self.backend_url)
                            tool_results.append({
                                "type": "tool_result",
                                "tool_use_id": block.id,
                                "content": json.dumps(result),
                            })
                    messages.append({"role": "user", "content": tool_results})
                    continue

                # Final turn: the text block is schema-valid JSON.
                final_text = next((b.text for b in response.content if b.type == "text"), "")
                metrics.record_request("ok")
                metrics.record_tool_iterations(iterations)
                return self._to_agent_response(final_text, data, tool_calls_made)

            logger.warning("agent did not converge within %d iterations", self.max_iterations)
            metrics.record_request("no_converge")
            metrics.record_tool_iterations(iterations)
            return AgentResponse(
                answer="Unable to reach a conclusion within the tool-call budget.",
                status="unknown",
                confidence=0.0,
                sources=_sources_from(data),
                tool_calls=tool_calls_made,
            )

        except Exception as e:  # noqa: BLE001 - surface any failure as a degraded response
            logger.error("agent reasoning failed: %s", e)
            metrics.record_request("error")
            return AgentResponse(answer=f"Error during reasoning: {e}", confidence=0.0)

    def _build_user_message(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any]
    ) -> str:
        """Build the user message for Claude based on intent and data."""
        return (
            f"Intent: {intent}\n"
            f"Context: {json.dumps(context, indent=2)}\n\n"
            f"Data already fetched for this query:\n{json.dumps(data, indent=2, default=str)}\n\n"
            "Analyze this data and answer the operator's question. If you need more "
            "detail, call a tool to fetch it; otherwise answer directly."
        )

    def _to_agent_response(
        self, final_text: str, data: Dict[str, Any], tool_calls: List[ToolCall]
    ) -> AgentResponse:
        """Validate the model's structured JSON and map it to an AgentResponse."""
        try:
            parsed = ReasoningOutput.model_validate_json(final_text)
        except ValidationError as e:
            logger.warning("structured output validation failed: %s", e)
            # Fall back to returning the raw text rather than dropping the answer.
            return AgentResponse(
                answer=final_text or "No answer produced.",
                confidence=0.3,
                sources=_sources_from(data),
                tool_calls=tool_calls,
            )

        return AgentResponse(
            answer=parsed.answer,
            status=parsed.status,
            confidence=_normalize_confidence(parsed.confidence),
            sources=_sources_from(data),
            tool_calls=tool_calls,
            details={
                "recommendations": parsed.recommendations,
                "findings": parsed.findings,
                "likely_cause": parsed.likely_cause,
                "severity": parsed.severity,
            },
        )


def _sources_from(data: Dict[str, Any]) -> List[str]:
    """Derive answer provenance from the pod IDs present in the fetched data."""
    return sorted(data.keys())


def _normalize_confidence(value: float) -> float:
    """Clamp confidence to 0.0–1.0, tolerating a model that answers on a 0–100 scale."""
    if value > 1.0:
        value = value / 100.0
    return max(0.0, min(1.0, value))


# Create a singleton instance
_agent: Optional[K8fyAgent] = None


def get_k8fy_agent() -> K8fyAgent:
    """Get the K8fy agent instance."""
    global _agent
    if _agent is None:
        _agent = K8fyAgent()
    return _agent
