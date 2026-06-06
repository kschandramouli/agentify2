# 0006 – Two-tier query path: deterministic fast-path + agentic synthesis path

## Status

Accepted   ·   2026-06-01

## Context

We now have a working end-to-end vertical slice (ingest → store → query → agent →
answer), validated live. That validation surfaced a concrete problem: **every
query is routed through the Claude API**, including deterministic structured
lookups. A "is the payment service healthy?" query took **~13.3s** and cost ~2
Opus calls — for a question that is a fixed rule (ready-ratio ≥ 75%).

Two facts make this wasteful:

1. The health/cert/metric verdicts are **deterministic functions of the data**,
   already implemented as pure functions in
   [`src/agent/k8fy/reasoning.py`](../../src/agent/k8fy/reasoning.py)
   (`evaluate_pod_health`, `evaluate_service_health`, `evaluate_cert_renewal`) —
   but they are currently **unused**; the LLM re-derives the verdict from raw
   data on every call.
2. An LLM in the synchronous hot path of *every* query is the dominant cost line
   and the worst p99 latency at scale, and it makes answers non-deterministic
   where operators want a stable result.

External design review independently flagged "LLM in the hot path of every
query" as the highest-confidence architectural concern.

This does **not** mean removing the LLM — most of its value (synthesis,
correlation, free-text "why did X break?") still needs it. It means routing.

## Decision

Split query handling into **two tiers**, chosen by the orchestrator:

- **Tier 1 — deterministic fast-path.** For intents with a known rule and
  sufficient data (`health_check`, `cert_check`, threshold `metrics_query`),
  compute the answer directly from the fetched store data using the pure-function
  evaluators. **No LLM call.** Format the response from a template. Fast (ms),
  free, and stable.
- **Tier 2 — agentic synthesis path.** For free-text, compound, ambiguous, or
  causal/"why" questions — and for any Tier-1 case where the rule cannot fully
  answer (missing data, multiple entities, follow-up) — use the LLM with tools
  (the existing Phase 2 agent loop). This is where correlation and narrative
  live.

The orchestrator picks the tier from the parsed intent; Tier 1 **falls through to
Tier 2** when its rule is inconclusive rather than returning a low-quality answer.

The Phase 2 agent tool-loop is **kept** — it becomes Tier 2, not dead code.

Every answer (both tiers) continues to carry `sources`; see the audit/provenance
item in [ROADMAP](../ROADMAP.md) for extending this into a full trace.

## Consequences

- **Positive:** deterministic queries become fast/free/stable; LLM spend is
  concentrated where it adds value; reuses evaluators that already exist; gives a
  deterministic fallback when the model/API is unavailable.
- **Negative / cost accepted:** two code paths to maintain; the tier-routing
  decision can itself be wrong (a "health" phrasing that is really a "why"
  question) — mitigated by Tier-1→Tier-2 fall-through. The evaluators currently
  live in Python (the agent); a true hot-path implementation may port them to Go
  (the backend, where the data is fetched) — that placement is an implementation
  detail deferred to the spec, not fixed here.
- **Revisit if:** the deterministic rule set grows unwieldy, or once tool-call
  budgets + an eval harness + semantic caching exist and we move toward fully
  agent-driven routing (the model chooses tools for everything). See
  [ROADMAP](../ROADMAP.md).

## Implementation pointer

Today the flow is: backend `inferIntent()` → route → fetch → **always** call
`/reason`. The change is to insert the Tier-1 branch after the fetch: if the
intent is deterministic and the data is sufficient, evaluate-and-format in-process
and return; otherwise call the agent (Tier 2). The exact rule set, fall-through
conditions, interfaces, and known limitations are specified in
[spec 003](../specs/003-tier1-deterministic-queries.md) (built; evaluators live in
Go at `internal/orchestrator/evaluator`).
