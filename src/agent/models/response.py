from pydantic import BaseModel
from typing import Optional, Dict, Any, List


class ToolCall(BaseModel):
    """A tool call made by the agent."""

    name: str
    arguments: Dict[str, Any]


class AgentResponse(BaseModel):
    """Response from the K8fy agent."""

    answer: str
    status: Optional[str] = None  # "healthy" | "degraded" | "unhealthy"
    confidence: float = 0.0
    sources: List[str] = []
    reasoning: Optional[str] = None  # Internal reasoning steps (for debugging)
    tool_calls: List[ToolCall] = []
    details: Dict[str, Any] = {}


class QueryRequest(BaseModel):
    """Request to the agent for reasoning."""

    intent: str
    data: Dict[str, Any]
    context: Dict[str, Any] = {}
    trace_id: Optional[str] = None  # propagated from the backend for cross-service correlation (spec 004)


class ReasoningOutput(BaseModel):
    """Structured output the model is constrained to emit (via output_config.format).

    Kept separate from AgentResponse: this is exactly what Claude returns, while
    AgentResponse is the service's wire shape (adds sources, tool_calls, etc.,
    which the agent fills in from provenance rather than the model).
    """

    answer: str
    status: str = "unknown"  # healthy | degraded | unhealthy | unknown | not_applicable
    confidence: float = 0.0  # 0.0–1.0
    recommendations: List[str] = []

    # Correlation / diagnosis fields (spec 005). Populated for `diagnose` intents;
    # empty/None for single-signal answers. They ride along in AgentResponse.details
    # for future UI consumers; the human-facing narrative still lives in `answer`.
    findings: List[str] = []  # one per signal considered (health, cert, ...)
    likely_cause: Optional[str] = None  # hypothesis; None when signals are insufficient
    severity: str = "info"  # info | warning | critical
