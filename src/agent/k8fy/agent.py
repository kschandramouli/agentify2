"""K8fy agent: Claude-powered Kubernetes operations reasoning."""

import json
import logging
from typing import Any, Dict, List, Optional

from anthropic import AsyncAnthropic
from pydantic import ValidationError

import metrics
from config.claude_client import get_claude_client
from config.settings import get_settings
from k8fy.prompt_manager import get_prompt
from k8fy.prompts import SYSTEM_PROMPT
from k8fy.tools import TOOLS, process_tool_call
from models.response import AgentResponse, ReasoningOutput, ToolCall

_DEFAULT_SYSTEM_PROMPT = get_prompt("k8fy/system", SYSTEM_PROMPT)
_DEFAULT_TOOLS = TOOLS

# Model pair for the advisor/executor strategy.
# Executor (EXECUTOR_MODEL) is the primary model — handles all tool calls cheaply.
# Advisor (ADVISOR_MODEL) is a server-side tool the executor consults mid-generation.
ADVISOR_MODEL = "claude-opus-4-8"
EXECUTOR_MODEL = "claude-sonnet-4-6"

_ADVISOR_BETA = "advisor-tool-2026-03-01"

# Timing guidance prepended to the executor's system prompt when the advisor
# tool is active. Based on the recommended system prompt from the advisor tool
# docs: https://platform.claude.com/docs/en/agents-and-tools/tool-use/advisor-tool
# Condensed for the K8fy diagnostic use case (research tasks, not coding).
_ADVISOR_TIMING_GUIDANCE = """\
You have access to an `advisor` tool backed by a stronger model. It takes NO \
parameters — when you call advisor(), your entire conversation history is \
automatically forwarded.

Call advisor BEFORE committing to a diagnostic approach, and again before \
producing your final answer. If orientation is needed first (checking which data \
is already available), do that, then call advisor.

Also call advisor when stuck or when considering a change of approach.

Give the advice serious weight. If empirical evidence from tool results contradicts \
a specific claim, surface the conflict in a follow-up advisor call rather than \
silently switching approach."""

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
    """K8fy agent for Kubernetes operations reasoning.

    Skills (spec 010) instantiate this with a focused system_prompt and a
    narrower tools list. Pass advisor_model + executor_model to enable the
    advisor/executor strategy via the built-in advisor_20260301 server-side
    tool. Omit them (leave advisor_model=None) for the single-model path.
    """

    def __init__(
        self,
        system_prompt: str = _DEFAULT_SYSTEM_PROMPT,
        tools: List[Dict[str, Any]] = _DEFAULT_TOOLS,
        advisor_model: Optional[str] = None,
        executor_model: Optional[str] = None,
    ):
        self.client: AsyncAnthropic = get_claude_client()
        self.model = settings.claude_model
        self.max_tokens = settings.claude_max_tokens
        self.effort = settings.claude_effort
        self.backend_url = settings.backend_url
        self.max_iterations = settings.agent_max_tool_iterations
        self._system_prompt = system_prompt
        self._tools = tools
        # Advisor/executor mode (advisor_model=None → single-model path).
        # The executor is the PRIMARY model; the advisor is a server-side tool
        # it consults mid-generation via client.beta.messages.create.
        self.advisor_model = advisor_model
        self.executor_model = executor_model or self.model

    async def reason(
        self, intent: str, data: Dict[str, Any], context: Optional[Dict[str, Any]] = None
    ) -> AgentResponse:
        """Reason about K8s operations given data and intent."""
        if context is None:
            context = {}
        if self.advisor_model:
            return await self._reason_advisor_executor(intent, data, context)
        return await self._reason_single(intent, data, context)

    # ------------------------------------------------------------------
    # Single-model path (original behaviour, unchanged)
    # ------------------------------------------------------------------

    async def _reason_single(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any]
    ) -> AgentResponse:
        """Agentic loop using one model for both reasoning and tool execution.

        Runs an agentic loop: Claude may call tools (which fetch more data from
        the backend) until it produces a final, schema-constrained answer.
        """
        # System prompt + tools are the stable cache prefix; cache_control caches
        # them together (tools render before system). Note: the prompt is small,
        # so on Opus 4.8 (4096-token cache minimum) it may not actually cache
        # until it grows — the markers are correct and cost nothing meanwhile.
        system = [{"type": "text", "text": self._system_prompt, "cache_control": {"type": "ephemeral"}}]
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
                    tools=self._tools,
                    messages=messages,
                )
                _record_loop_usage(response, self.model)

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

        except Exception as e:  # noqa: BLE001
            logger.error("agent reasoning failed: %s", e)
            metrics.record_request("error")
            return AgentResponse(answer=_user_error_message(e), status="error", confidence=0.0)

    # ------------------------------------------------------------------
    # Advisor/executor path — built-in advisor_20260301 server-side tool
    # ------------------------------------------------------------------

    async def _reason_advisor_executor(
        self, intent: str, data: Dict[str, Any], context: Dict[str, Any]
    ) -> AgentResponse:
        """Agentic loop using the built-in advisor_20260301 server-side tool.

        The executor model (self.executor_model, e.g. Sonnet 4.6) is the PRIMARY
        model and handles all K8fy tool calls. The advisor model (self.advisor_model,
        e.g. Opus 4.8) is a server-side tool declared in the tools list; the
        executor consults it mid-generation for strategic guidance.

        Everything runs inside a single /v1/messages call per iteration — no manual
        two-phase split. The server orchestrates the advisor sub-inference; the
        client only executes K8fy tool_use blocks (type=="tool_use"), not the
        advisor (type=="server_tool_use", already handled server-side).

        advisor_tool_result blocks arrive fully formed in response.content and must
        be passed back verbatim on subsequent turns (included automatically when we
        append response.content to messages).

        Cost profile: executor tokens at Sonnet rates + advisor tokens at Opus rates
        (reported separately in usage.iterations, not in top-level usage).
        """
        # Timing guidance must be prepended before the skill prompt so the executor
        # knows when to call the advisor.
        advisor_system_text = _ADVISOR_TIMING_GUIDANCE + "\n\n" + self._system_prompt
        system = [{"type": "text", "text": advisor_system_text, "cache_control": {"type": "ephemeral"}}]

        # Soft-limit on advisor output length (docs: ask for ~80% of true ceiling).
        # Placed in the user message so the advisor sees it as a direct instruction.
        user_message = (
            self._build_user_message(intent, data, context)
            + "\n\n(Advisor: please keep your guidance under 150 words — focused diagnosis, not a comprehensive plan.)"
        )

        # Advisor tool definition.
        # max_tokens=2048: docs recommend this as the starting point; reduces mean
        #   advisor output ~7x vs. unset with near-zero truncation.
        # max_uses=3: per-request cap; executor continues without advice once hit.
        # caching: saves cost when the advisor is called 3+ times per conversation;
        #   the cache prefix is stable across calls because each call extends the
        #   previous transcript by one segment.
        advisor_tool: Dict[str, Any] = {
            "type": "advisor_20260301",
            "name": "advisor",
            "model": self.advisor_model,
            "max_tokens": 2048,
            "max_uses": 3,
            "caching": {"type": "ephemeral", "ttl": "5m"},
        }
        tools = [advisor_tool, *self._tools]

        messages: List[Dict[str, Any]] = [{"role": "user", "content": user_message}]
        tool_calls_made: List[ToolCall] = []
        iterations = 0

        try:
            for _ in range(self.max_iterations):
                iterations += 1
                response = await self.client.beta.messages.create(
                    betas=[_ADVISOR_BETA],
                    model=self.executor_model,
                    max_tokens=self.max_tokens,
                    system=system,
                    thinking={"type": "adaptive"},
                    output_config={"effort": self.effort, "format": {"type": "json_schema", "schema": REASONING_SCHEMA}},
                    tools=tools,
                    messages=messages,
                )
                # Advisor tokens are in usage.iterations (type:"advisor_message"),
                # not in the top-level usage totals.
                _record_loop_usage(response, self.executor_model, self.advisor_model)

                if response.stop_reason == "tool_use":
                    # Preserve the full assistant turn including any server_tool_use
                    # and advisor_tool_result blocks — they must be passed back
                    # verbatim on the next turn.
                    messages.append({"role": "assistant", "content": response.content})
                    tool_results = []
                    for block in response.content:
                        # Only execute K8fy tool_use blocks. server_tool_use blocks
                        # (type=="server_tool_use") are handled server-side; their
                        # advisor_tool_result counterparts are already in response.content.
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

                final_text = next((b.text for b in response.content if b.type == "text"), "")
                metrics.record_request("ok")
                metrics.record_tool_iterations(iterations)
                return self._to_agent_response(final_text, data, tool_calls_made)

            logger.warning("executor did not converge within %d iterations", self.max_iterations)
            metrics.record_request("no_converge")
            metrics.record_tool_iterations(iterations)
            return AgentResponse(
                answer="Unable to reach a conclusion within the tool-call budget.",
                status="unknown",
                confidence=0.0,
                sources=_sources_from(data),
                tool_calls=tool_calls_made,
            )

        except Exception as e:  # noqa: BLE001
            logger.error("advisor/executor reasoning failed: %s", e)
            metrics.record_request("error")
            return AgentResponse(answer=_user_error_message(e), status="error", confidence=0.0)

    # ------------------------------------------------------------------
    # Pattern A — pre-fetch then single call
    # ------------------------------------------------------------------

    async def _fetch(self, tool_name: str, args: Dict[str, Any]) -> Dict[str, Any]:
        """Call one tool against the backend and return its result dict."""
        return await process_tool_call(tool_name, args, self.backend_url)

    async def _reason_pattern_a(
        self,
        intent: str,
        data: Dict[str, Any],
        context: Dict[str, Any],
        prefetched: Dict[str, Any],
    ) -> AgentResponse:
        """Pattern A: one Claude call over pre-assembled data, no tool loop.

        The caller is responsible for fetching whatever additional data is
        needed (via _fetch / asyncio.gather) and passing it as `prefetched`.
        That dict is merged into `data` before the prompt is built, so Claude
        sees the full picture in a single turn.

        No tools are declared in the request — the prompt tells Claude that
        all data has been pre-fetched. This eliminates the agentic loop
        entirely for intents whose data requirements are fully predictable.

        Cost profile: N parallel backend fetches + exactly 1 Claude call.
        """
        merged = {**data, **prefetched}
        system = [{"type": "text", "text": self._system_prompt, "cache_control": {"type": "ephemeral"}}]
        # Append a direct instruction so Claude doesn't wait for tool calls
        # that will never come.
        user_content = (
            self._build_user_message(intent, merged, context)
            + "\n\nAll relevant data has been pre-fetched and is included above. "
            "Produce your final answer directly."
        )

        try:
            response = await self.client.messages.create(
                model=self.model,
                max_tokens=self.max_tokens,
                system=system,
                thinking={"type": "adaptive"},
                output_config={"effort": self.effort, "format": {"type": "json_schema", "schema": REASONING_SCHEMA}},
                messages=[{"role": "user", "content": user_content}],
            )
            _record_loop_usage(response, self.model)
            final_text = next((b.text for b in response.content if b.type == "text"), "")
            metrics.record_request("ok")
            metrics.record_tool_iterations(0)  # 0 = pattern A, no loop
            return self._to_agent_response(final_text, merged, [])
        except Exception as e:  # noqa: BLE001
            logger.error("pattern-a reasoning failed: %s", e)
            metrics.record_request("error")
            return AgentResponse(answer=_user_error_message(e), status="error", confidence=0.0)

    # ------------------------------------------------------------------
    # Shared helpers
    # ------------------------------------------------------------------

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


# ------------------------------------------------------------------
# Module-level helpers
# ------------------------------------------------------------------

def _user_error_message(e: Exception) -> str:
    """Return a user-facing error message that never leaks raw API responses."""
    s = str(e)
    if "rate_limit" in s or "429" in s:
        return "Rate limit reached — too many requests in flight. Please wait a moment and try again."
    if "credit balance" in s.lower() or "billing" in s.lower():
        return "AI service unavailable — the API account has insufficient credits. Please contact your administrator."
    if "timeout" in s.lower() or "timed out" in s.lower():
        return "Request timed out — the query took too long. Try a more specific question or retry."
    if "overloaded" in s.lower() or "529" in s:
        return "The AI service is temporarily overloaded. Please retry in a few seconds."
    return "Analysis failed — an unexpected error occurred. Please try again."


def _record_loop_usage(
    response,
    executor_model: str,
    advisor_model: Optional[str] = None,
) -> None:
    """Record per-iteration token usage, separating executor and advisor turns.

    With the advisor tool active, advisor tokens appear only in usage.iterations
    (type: "advisor_message") and are billed at the advisor model's rates.
    Top-level usage totals reflect executor tokens only.
    """
    usage = getattr(response, "usage", None)
    if usage is None:
        return
    iterations = getattr(usage, "iterations", None)
    if iterations:
        for iteration in iterations:
            itype = getattr(iteration, "type", "message")
            model = advisor_model if (advisor_model and itype == "advisor_message") else executor_model
            metrics.record_usage(model, iteration)
    else:
        metrics.record_usage(executor_model, usage)


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
