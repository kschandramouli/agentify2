# 0011 – Self-observability: Prometheus metrics for the pipeline

## Status

Accepted · 2026-06-03

## Context

[ROADMAP P3c](../ROADMAP.md): an observability product with no internal
observability won't survive its own incidents. Today the backend emits structured
slog logs (request latency, routing, ingest, errors) but **no metrics** — there's
no way to see the Tier-1/Tier-2 split, LLM-call health, ingest throughput, or
registry failures at a glance.

## Decision

Add a **Prometheus metrics layer** to the Go backend (v1 — backend only).

- **Pull-based `/metrics` endpoint** (`promhttp`), not push. Zero external
  dependency, works locally and in any environment; matches the "Prometheus local"
  choice in CLAUDE.md. CloudWatch/remote-write is a deployment concern layered on later.
- **Domain metrics first** (the signal unique to agentify), all with **bounded
  labels** to avoid cardinality blowups:

  | Metric | Type | Labels | Why |
  |---|---|---|---|
  | `agentify_queries_total` | counter | intent, tier, status | the **Tier-1 vs Tier-2 split** — cost/latency posture (ADR 0006) |
  | `agentify_query_duration_seconds` | histogram | tier | deterministic (~ms) vs agentic (~s) |
  | `agentify_agent_calls_total` | counter | status | Tier-2 LLM round-trip health |
  | `agentify_agent_call_duration_seconds` | histogram | — | LLM latency as seen by the backend |
  | `agentify_ingest_total` | counter | store_type, result | ingest throughput + failures |
  | `agentify_http_requests_total` | counter | method, path, code | generic HTTP |
  | `agentify_http_request_duration_seconds` | histogram | method, path | generic HTTP |

  Labels are **only** bounded sets (intents, tiers, statuses, store types, fixed
  route paths, status codes). **Never** label by pod/entity/namespace id.
- **Free Go-runtime/process metrics** come with the default Prometheus registry.
- Registry failures surface via `agentify_ingest_total{result="error"}` and
  `agentify_queries_total{status="error"}` (no separate registry metric in v1).

## Consequences

- **Positive:** one scrape shows pipeline health (tier mix, LLM latency/errors,
  ingest rate) — the metrics you'd actually want mid-incident; portable; cheap.
- **Negative / cost accepted:** a metrics dependency + one endpoint; **agent-side
  token/cost is not captured** (it lives in the Python agent — the highest-value
  follow-up). No tracing, so cross-service correlation of a single request is still
  log-archaeology.
- **Agent-side token/cost — done (2026-06-03):** the Python agent now exposes its
  own `/metrics` (`agent_model_tokens_total{model,type}`, `agent_requests_total`,
  `agent_tool_iterations`, and an indicative `agent_estimated_cost_usd_total`).
  Token counts are authoritative; the USD figure comes from a static, clearly-marked
  price table — production should derive cost from the token counters via a PromQL
  recording rule, not trust the in-code prices.
- **Deferred (documented):** request correlation IDs + distributed tracing
  (OpenTelemetry/X-Ray); CloudWatch remote-write; dashboards/alerts. And the
  **pod-registry SPOF** itself (caching/stale-entry handling) is a *resilience*
  decision, separate from this instrumentation — tracked in ROADMAP P3c.
- **Revisit if:** label cardinality grows (e.g. routes gain path-param IDs — switch
  to route patterns), or we need cross-service traces (add OpenTelemetry).
