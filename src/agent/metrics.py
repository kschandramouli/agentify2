"""Prometheus metrics for the agent (token usage + indicative cost). See ADR 0011.

Token counts are the **authoritative facts**. The USD figure is derived from a
static, indicative price table below — convenient for an at-a-glance number, but
NOT the system of record: in production compute cost from the token counters via a
PromQL recording rule, so prices live in one place and stay current.
"""

from prometheus_client import CONTENT_TYPE_LATEST, Counter, Histogram, generate_latest

# type ∈ input | output | cache_read | cache_write
MODEL_TOKENS = Counter(
    "agent_model_tokens_total",
    "Model token usage, by model and token type.",
    ["model", "type"],
)

REQUESTS = Counter(
    "agent_requests_total",
    "Agent reason() requests, by outcome.",
    ["outcome"],  # ok | error | no_converge
)

TOOL_ITERATIONS = Histogram(
    "agent_tool_iterations",
    "Tool-loop iterations per reason() request.",
    buckets=(1, 2, 3, 4, 5, 8),
)

ESTIMATED_COST = Counter(
    "agent_estimated_cost_usd_total",
    "Indicative model cost in USD from a static price table (see module docstring).",
    ["model"],
)

# USD per token. INDICATIVE — keep in sync with Anthropic pricing; do not treat as
# authoritative. cache_read ≈ 0.1× input, cache_write ≈ 1.25× input.
_PRICES = {
    "claude-opus-4-8": {
        "input": 5e-6,
        "output": 25e-6,
        "cache_read": 0.5e-6,
        "cache_write": 6.25e-6,
    },
}
_DEFAULT_PRICES = _PRICES["claude-opus-4-8"]


def record_usage(model: str, usage) -> None:
    """Record token counts + indicative cost from a Claude response's usage object."""
    if usage is None:
        return
    counts = {
        "input": getattr(usage, "input_tokens", 0) or 0,
        "output": getattr(usage, "output_tokens", 0) or 0,
        "cache_read": getattr(usage, "cache_read_input_tokens", 0) or 0,
        "cache_write": getattr(usage, "cache_creation_input_tokens", 0) or 0,
    }
    for ttype, n in counts.items():
        if n:
            MODEL_TOKENS.labels(model=model, type=ttype).inc(n)

    prices = _PRICES.get(model, _DEFAULT_PRICES)
    cost = sum(counts[t] * prices[t] for t in counts)
    if cost:
        ESTIMATED_COST.labels(model=model).inc(cost)


def record_request(outcome: str) -> None:
    REQUESTS.labels(outcome=outcome).inc()


def record_tool_iterations(n: int) -> None:
    TOOL_ITERATIONS.observe(n)


def exposition() -> tuple[bytes, str]:
    """Return (body, content_type) for the /metrics endpoint."""
    return generate_latest(), CONTENT_TYPE_LATEST
