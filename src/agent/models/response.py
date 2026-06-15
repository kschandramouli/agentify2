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

    # Token usage + indicative cost for this single agent call (Tier-2 only).
    # Populated by _reason_pattern_a(); zero for Tier-1 fast-path answers.
    input_tokens: int = 0
    output_tokens: int = 0
    estimated_cost_usd: float = 0.0


class QueryRequest(BaseModel):
    """Request to the agent for reasoning."""

    intent: str
    data: Dict[str, Any]
    context: Dict[str, Any] = {}
    trace_id: Optional[str] = None  # propagated from the backend for cross-service correlation (spec 004)


class FindingDetail(BaseModel):
    """Structured finding from the k8fy/health-check prompt (new format)."""
    resource: str
    status: str  # HEALTHY | DEGRADED | UNHEALTHY
    reason: str


class ServiceHealthDetail(BaseModel):
    """Per-service health summary from the k8fy/health-check prompt (new format)."""
    service: str
    ready_replicas: int = 0
    total_replicas: int = 0
    ready_percent: float = 0.0
    endpoints: int = 0


class ReasoningOutput(BaseModel):
    """Structured output the model is constrained to emit (via output_config.format).

    Kept separate from AgentResponse: this is exactly what Claude returns, while
    AgentResponse is the service's wire shape (adds sources, tool_calls, etc.,
    which the agent fills in from provenance rather than the model).

    Supports two prompt formats:
    - Old (k8fy/system, k8fy/diagnose, etc.): answer field is populated.
    - New (k8fy/health-check): headline + summary are populated; answer defaults "".
    """

    # Old-format answer field; optional so the new health-check schema (which omits
    # it) still validates. In _to_agent_response, headline takes precedence.
    answer: str = ""
    status: str = "unknown"  # healthy | degraded | unhealthy | unknown | not_applicable
    confidence: float = 0.0  # 0.0–1.0
    recommendations: List[str] = []

    # New k8fy/health-check fields (empty/None for old-format responses).
    headline: str = ""        # e.g. "🟢 checkout-api healthy (5/5 pods ready)"
    summary: str = ""         # ≤40-word prose summary
    service_health: Optional[ServiceHealthDetail] = None

    # findings: str for old format, FindingDetail for new health-check format.
    findings: List[Any] = []
    likely_cause: Optional[str] = None
    severity: str = "info"  # info | warning | critical
