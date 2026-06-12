# agentify — Roadmap & Backlog

> Prioritized record of decisions/work **not yet acted on**, so they aren't lost.
> Seeded from an external design review (2026-06-01) plus our own assessment.
> The one item being acted on *now* — the two-tier query path — is recorded as an
> accepted decision in [ADR 0006](decisions/0006-two-tier-query-path.md), not here.

## Important correction to the review's premise

The review assessed the **docs only** ("no repo attached") and concluded there was
no working vertical slice. As of 2026-06-01 there **is** one: `ingest → store →
query → agent → answer` was validated live (K8s-shaped event → pod formation →
Redis → routed query → Opus 4.8 → correct health verdict). So the review's #1
("stop everything, build one slice") is already done; the items below are about
**hardening and cutting**, not first-time build.

## Priority ladder

| # | Item | Status | Lands in |
|---|------|--------|----------|
| **P1** | Two-tier query path (deterministic fast-path + agentic) | **✅ Done (validated 2026-06-01: health query 13.3s→1ms, 0 LLM calls)** | [ADR 0006](decisions/0006-two-tier-query-path.md) |
| **P2a** | Egress / redaction / data-governance gate | **✅ v1 done (2026-06-01: allowlist redaction live; in-region client = follow-up)** | [ADR 0007](decisions/0007-egress-data-governance.md) + [policy](policies/data-governance.md) |
| **P2b** | Collapse storage to a single **Postgres** store (Redis removed; pgvector deferred) | **✅ v1 done (2026-06-02: current_state+events tables; validated on real PG via embedded-postgres)** | [ADR 0010](decisions/0010-postgres-single-store.md) + [storage-strategy](policies/storage-strategy.md) |
| **P2c** | Multi-provider / per-tenant model routing (in-region: Bedrock/Vertex/Foundry) | Proposed — **deferred until a client requires it** | [ADR 0008](decisions/0008-multi-provider-model-routing.md); now per-**deployment** (P3a resolved) |
| **P3a** | Multi-tenancy / isolation model | **✅ Resolved (ADR 0009): single-tenant per deployment — no in-app multi-tenancy** | [ADR 0009](decisions/0009-tenancy-single-tenant-per-deployment.md) |
| **P3b** | Audit / answer provenance (trace_id + structured `query.trace`) | **✅ v1 done (2026-06-03: trace_id returned + propagated; retrieval API deferred)** | [spec 004](specs/004-query-provenance.md) |
| **P3c** | Self-observability (instrument the pipeline) | **✅ v1 done (2026-06-03: Prometheus /metrics — tier split, LLM, ingest, HTTP)** | [ADR 0011](decisions/0011-self-observability-metrics.md) |
| **P4a** | Agentic root-cause correlation (deepen Tier 2) | **✅ v1 done (2026-06-04: `diagnose` intent + multi-signal fan-out → Tier-2 correlation; findings/likely_cause/severity; live-validated)** | [spec 005](specs/005-root-cause-correlation.md), [correlation](policies/correlation.md) |
| **P4b** | Temporal spine (restart time-series → causal diagnosis); classical ML deferred | **✅ spine done (2026-06-05: k8fy.metrics append-only samples, windowed query, get_metrics_history tool); ML still Proposed** | [spec 006](specs/006-temporal-ingestion-and-history.md), [ADR 0013](decisions/0013-temporal-data-in-postgres-events-table.md) |
| **P4b-ops** | Operational context for causal diagnosis: deploy/change events + on-demand pod logs | **✅ v1 done (2026-06-05: `k8fy.events` deploy events + `get_change_history`; ephemeral redacted log tail via `get_pod_logs`; live-validated deploy↔onset correlation). Hardened 2026-06-06: events-table retention janitor ([ADR 0015](decisions/0015-events-table-retention.md)) + bearer-token auth on the adapter `/logs` surface.** | [spec 007](specs/007-change-events.md), [spec 008](specs/008-on-demand-pod-logs.md), [ADR 0014](decisions/0014-on-demand-ephemeral-log-fetch.md) |
| **P4c** | Investigation-on-anomaly loop (human-in-loop, **no** auto-remediation) | **✅ v1 done (2026-06-06: opt-in periodic deterministic sweep → diagnose → Slack-compatible webhook; namespace incident dedup + cooldown + per-sweep cap; redacted egress; read-only)** | [spec 009](specs/009-investigation-on-anomaly.md), [ADR 0016](decisions/0016-proactive-investigation-loop.md); respects [ADR 0003](decisions/0003-read-only-to-actions-boundary.md) |
| **P5** | Pattern A standardisation across all skill classes (deterministic pre-fetch + single Claude call per intent) | **✅ Done (2026-06-11: all 5 skills on Pattern A; DiagnoseSkill advisor/executor removed; [ADR 0017](decisions/0017-pattern-a-skills-standardisation.md))** | [spec 010](specs/010-skill-router.md), [ADR 0017](decisions/0017-pattern-a-skills-standardisation.md) |
| **P5+** | Supporting tooling: AI gateway (semantic cache/budgets), eval harness + tool-call budgets, agent tracing | Later | ops/spec |

---

## P2a — Egress / redaction / data-governance gate

**Review (confidence: Certain):** the K8fy adapter holds a ClusterRole that reads
Secrets, and the flow ships raw pod data (secret names, namespaces, cert metadata,
possibly payloads) to an external API with no redaction — a procurement-killing
finding for enterprise security review. Make egress destinations configurable
(some customers require Bedrock/in-region or a self-hosted model).

**Our assessment:** Agree, and it's the top *enterprise-gating* item. We confirmed
the flow sends raw fetched pod data to Anthropic with zero redaction. Two-tier
(P1) helps a little — deterministic answers never leave the boundary — but Tier 2
still egresses. **Build a redaction/classification gate before anything reaches a
model, and make the model endpoint pluggable** (first-party / Bedrock / Vertex /
Foundry — Claude is **not** self-hostable; see [P2c](#p2c--multi-provider-model-routing)).
Not blocking the working slice; blocking any enterprise pilot.

**Status (v1 done, 2026-06-01):** allowlist redaction implemented at the backend→
agent boundary (`internal/governance`, applied in `/api/query` + `/api/agent/fetch`),
validated live (secrets in payload dropped before egress), unit-tested. Endpoint is
overridable via `ANTHROPIC_BASE_URL`. **Still open:** first-class in-region clients
(Bedrock/Vertex/Foundry — see [P2c](#p2c--multi-provider-model-routing)); redacting
the operator's free-text question; per-tenant classification (needs P3a). See
[ADR 0007](decisions/0007-egress-data-governance.md) and [policy](policies/data-governance.md).

## P2b — Storage consolidation (Postgres + pgvector spine)

**Review (Likely/Certain):** real data shapes are three (append-only/time-series,
current-state snapshots, free text), not six. Run Postgres + pgvector as the spine
(relational + JSONB + vector in one transactional system; ceiling ~50M vectors,
far above us), drop Weaviate/Redis from the MVP, add S3+Parquet+DuckDB for cheap
history, and only adopt ClickHouse/Timescale or a dedicated vector DB (Qdrant/
Milvus) when volume/filtered-ANN-at-scale justifies it.

**Our assessment:** Agree on the implementation; **keep the pod-mesh concept** (the
product's brain — ADRs [0001](decisions/0001-adopt-context-mesh-architecture.md),
[0002](decisions/0002-pods-are-recursive.md)). The review slightly conflates
"6 stores" with the mesh idea — they're separable. Collapsing the engines makes
`store_type` mostly `relational` and shrinks the trait→store matrix to a routing
detail, which also removes most of the justification for an auto refinement-loop
(a feature deletion = a win). We already felt this pain: the polyglot stack
wouldn't run locally. Sequencing note: P3a is resolved
([ADR 0009](decisions/0009-tenancy-single-tenant-per-deployment.md)) —
**single-tenant per deployment**, so the Postgres schema is single-tenant (no
`tenant_id`/RLS).

**Status (v1 done, 2026-06-02, [ADR 0010](decisions/0010-postgres-single-store.md)):**
collapsed to **one Postgres** — `current_state` table (kv / latest-wins, replaced
Redis) + `events` table (relational). Redis removed from runtime, config, deps, and
package tree. **pgvector deferred** (no semantic-search feature — YAGNI); Weaviate
left inert as the documented future vector option. Validated on a real Postgres via
`embedded-postgres` (no Docker). **Still open:** pgvector when similarity search
lands; S3+Parquet+DuckDB cold history; ClickHouse/Timescale at time-series volume.

## P2c — Multi-provider model routing

**Why:** at multi-client scale, clients differ on data residency, compliance,
cloud, and billing. "Self-hosted Claude" is not possible; the in-boundary options
are provider-operated: **Bedrock/Vertex/Foundry** (data in the customer's cloud
account/region) or **Claude Platform on AWS** (full features + AWS billing, but
inference is Anthropic-operated — data may leave AWS).

**Our assessment:** verified (2026-06-02) that **agentify uses only the portable
Messages-API surface** (tool use, structured outputs, caching, adaptive thinking) —
all supported on Bedrock; it uses no Managed Agents/server-side tools. So Bedrock is
viable with **zero functional loss**. Recommended shape: a per-tenant client factory
`{provider, region, model_id, credentials}`; keep agentify on the portable surface
to preserve portability; prefer **BYO-cloud** billing (client's own Bedrock/Vertex
account) for data-sensitive clients.

**Status:** **deferred — do not build until a paying client requires it.** Gated on
P3a (provider/region/creds are tenant attributes). Full analysis in
[ADR 0008](decisions/0008-multi-provider-model-routing.md).

## P3a — Multi-tenancy / isolation model

**Review (Certain):** no tenant concept anywhere; "namespace" is a K8s namespace,
not a tenant boundary. Retrofitting tenancy after the schema is set is one of the
most expensive migrations there is — pick row-level vs schema vs DB-per-tenant now.

**Our assessment / Resolution (2026-06-02, [ADR 0009](decisions/0009-tenancy-single-tenant-per-deployment.md)):**
The review's framing assumed a shared SaaS. It isn't: agentify is **single-tenant
per deployment** (confirmed), so the deployment *is* the isolation boundary and
**no in-app multi-tenancy is built** — no `tenant_id` rows, no RLS, no per-tenant
schemas. A constant `DEPLOYMENT_ID` seam is reserved for fleet observability + a
future migration path. Row-level isolation is the menu to revisit **only if** the
GTM shifts to shared multi-tenant SaaS — and it must be done before P2b's schema
hardens. Cost accepted: fleet ops (N deployments).

## P3b — Audit / answer provenance

**Review:** enterprises will ask "why did the AI say that, and what did it see?"
We return `sources` already; extend to a queryable trace of every fetch + prompt.

**Our assessment:** Agree, incremental. Natural extension of the `sources` field
both tiers already emit (ADR 0006).

**Status (v1 done, 2026-06-03, [spec 004](specs/004-query-provenance.md)):** every
`/api/query` returns a `trace_id` and emits one structured `query.trace` log
(question, intent, tier, sources, status, confidence, tool calls, latency). The
trace_id is propagated to the agent (which logs it) — one correlation spine across
both services (this also closed the correlation-ID gap deferred in P3c). Provenance
shows the **redacted** view (sources + tool calls), not raw data. **Deferred:** a
Postgres-persisted trace table + `GET /admin/traces/{id}` retrieval API (largely
duplicates log search; adds retention burden); agent-side **exact-prompt** capture;
trace retention policy.

## P3c — Self-observability

**Review (high prior):** an observability product with no internal observability
won't survive its own incidents. Instrument the pipeline before adding features.
Also: the pod registry is a single source of truth / likely SPOF — define caching,
failure behavior, and stale/missing-entry handling.

**Our assessment:** Agree. Pair metrics with the structured logging we already emit.

**Status (v1 done, 2026-06-03, [ADR 0011](decisions/0011-self-observability-metrics.md)):**
Prometheus `/metrics` (pull-based, bounded labels, excluded from its own middleware)
with domain metrics — `agentify_queries_total{intent,tier,status}` (the Tier-1/Tier-2
split), query + agent-call latency, `agentify_ingest_total`, and HTTP metrics — plus
free Go-runtime metrics. Validated live. **Agent-side token/cost done (2026-06-03):**
the Python agent exposes its own `/metrics` (`agent_model_tokens_total{model,type}`,
request/iteration counters, indicative `agent_estimated_cost_usd_total`). **Still
open:** request correlation IDs done (spec 004); distributed tracing; CloudWatch
remote-write; dashboards/alerts. **Pod-registry resilience — done (2026-06-03,
[ADR 0012](decisions/0012-pod-registry-cache.md)):** a read-through snapshot cache
over the DynamoDB registry — eliminates per-query Scans, serves stale on a registry
blip (instead of 500s), invalidates on pod formation, and exposes
`agentify_registry_cache_total{result}`.

## P4 — Capability expansion (after foundations)

- **P4a Root-cause correlation — ✅ v1 done (2026-06-04):** a `diagnose` intent
  ([spec 005](specs/005-root-cause-correlation.md)) recognized before health/cert
  fans out to all of a service's k8fy signals; the Tier-2 agent synthesizes one
  causal narrative (active incident → latent risk → likely cause → prioritized
  actions) with structured `findings`/`likely_cause`/`severity`. **Bound:** v1
  correlates current-state **health + certs** only — temporal root cause ("crashed
  because of the 3pm deploy") needs the event/metric pipeline (P4b) and is deferred.
  The agent honors this: when no events are available it says so rather than
  inventing a cause (live-validated).
- **P4b Temporal spine — ✅ v1 done (2026-06-05):** the prerequisite that makes
  diagnosis *causal*. Restart counts are now emitted as **append-only samples**
  (`k8fy.metrics`) instead of latest-wins, persisted in the Postgres events table
  ([ADR 0013](decisions/0013-temporal-data-in-postgres-events-table.md)); the events
  store gained windowed/entity/order/limit queries; the agent has a
  `get_metrics_history` tool and uses the restart **trend** (when it began) in
  diagnosis ([spec 006](specs/006-temporal-ingestion-and-history.md)).
  **Bounds (deferred):** restarts only (no CPU/mem — needs metrics-server); no
  lifecycle-event capture yet (watch-event noise); no deploy/change correlation; no
  retention job.
- **P4b-ML Classical ML (still Proposed):** time-series anomaly detection, log
  template extraction (Drain-style) to collapse log volume before it hits a model,
  embeddings for semantic event search (pairs with pgvector), trivial forecasting
  for cert/capacity. **Principle: deterministic tool for the deterministic job, LLM
  for synthesis only** — we already compute `days_until_expiry` deterministically;
  the LLM must never do that arithmetic. Now unblocked by the temporal spine, but
  not justified until there's sample volume.
- **P4c Investigation-on-anomaly loop:** anomaly fires → agent gathers context →
  posts a summary to Slack/PagerDuty. **Human-in-the-loop; no auto-remediation** —
  consistent with [ADR 0003](decisions/0003-read-only-to-actions-boundary.md).

## P5 — Pattern A skills standardisation ✅ Done (2026-06-11)

All five skill classes (`HealthSkill`, `CertAuditSkill`, `ChangeHistorySkill`,
`RestartTrendSkill`, `DiagnoseSkill`) now use Pattern A: deterministic parallel
pre-fetch of all predictable signals + exactly one Claude call per request. No tools
are declared to Claude; data is injected directly into the user message. The
advisor/executor strategy (`advisor_20260301` beta) in `DiagnoseSkill` is removed.
See [ADR 0017](decisions/0017-pattern-a-skills-standardisation.md) and
[spec 010](specs/010-skill-router.md) for the full implementation record.

## P5+ — Supporting tooling (when scaling)

### Tools vs Skills — context (historical)

**Tools** are atomic, stateless functions the LLM calls during reasoning to fetch
data. We already have seven: `get_service_health`, `get_pod_logs`,
`get_metrics_history`, `get_change_history`, `get_pod_events`, `get_certificates`,
`query_pod` (all in `src/agent/k8fy/tools.py`).

**Skills** are higher-level, pre-packaged diagnostic workflows that combine multiple
tools, have their own specialised system prompt, and return a structured result.
A skill knows *how* to solve a class of problem — not just how to fetch one piece of
data. The current general-purpose K8fy agent is a single "know-everything" context;
skills split it into expert specialists.

#### Pattern A — Hardcoded tool sequences (deterministic skill, lower cost)

Pre-assemble data with a fixed tool sequence, then make exactly ONE Claude call
with everything pre-loaded. Bypasses the ad-hoc tool loop entirely:

```python
# src/agent/k8fy/skills/diagnose_crash.py
async def diagnose_crash_loop(pod_id, namespace) -> DiagnosisResult:
    logs    = await process_tool_call("get_pod_logs",        {"pod_id": pod_id, "previous": True})
    events  = await process_tool_call("get_pod_events",      {"pod_id": pod_id})
    metrics = await process_tool_call("get_metrics_history", {"pod_id": pod_id})
    # ONE Claude call with all data pre-assembled → predictable cost
    return await reason_over(logs, events, metrics, CRASH_DIAGNOSIS_PROMPT)
```

Cost: exactly 1 Opus call + 3 tool fetches regardless of how complex the crash is.
The current ad-hoc loop takes 2–7 tool iterations for the same problem.
**Recommended first step — immediately implementable, 30–50% token cost reduction.**

#### Pattern B — Sub-agent with specialised prompt (agentic skill, higher quality)

A separate Claude instance with domain expertise baked into its system prompt.
A **skill router** (using the Tier-1 findings that already exist) dispatches to
the right specialist:

```
User: "why is payment-worker crashing?"
         ↓
   Tier-1 evaluator → finds CrashLoopBackOff
         ↓
   SkillRouter (new — reads Tier-1 findings):
     crash detected     → CrashLoopSkill    (prompt: K8s failure-mode expert)
     cert expiry        → CertAuditSkill    (prompt: PKI/TLS lifecycle expert)
     rollout regression → DeploymentSkill   (prompt: rollout strategy expert)
```

Each skill has a focused system prompt that makes Claude sharper for its domain,
reducing hallucination and irrelevant context. The router lives naturally at the
same point where Tier-1 currently hands off to Tier-2 (`handlers.go`
`tryDeterministic` → agent call).

**Why Pattern B fits agentify specifically:**
- The Tier-1 evaluator already classifies the problem type *before* Claude is called.
  That classification is exactly the input a skill router needs.
- Different failure modes need different expertise: crash-loop root cause ≠ PKI
  lifecycle ≠ deployment rollout analysis.
- The skill boundary enforces a tool-call budget per skill class (a crash skill
  always calls logs + events + metrics; never more).
- Aligns with the existing intent taxonomy (`health_check`, `cert_check`,
  `diagnose`, `change_history`) — one skill per intent class.

**Implementation order:** Pattern A first (lower risk, immediate savings), then
Pattern B (higher quality, requires skill router + per-domain prompts).

### Other P5 items

- **AI gateway** — semantic caching (cache hits ~5ms vs ~2s full round-trip),
  fallback model routing (Haiku for simple intents, Opus only for synthesis),
  per-namespace cost budgets.
- **Eval harness** — regression test suite for Tier-2 answer quality: fixed
  query/ground-truth pairs, run on CI after prompt or model changes. Prevents
  silent quality regressions. Tools: Langfuse or MLflow for tracing + eval.
  Prerequisite: skills (Pattern A/B) must be stable before evals are meaningful.
- **Agent tracing** — per-call tool-iteration counts, latency, and token cost
  surfaced in the UI alongside the answer (partial: Prometheus metrics exist for
  token counts; structured per-call trace still deferred).
- **Tool-call budgets** — hard cap on tool iterations per skill class. Pattern A
  naturally enforces this; Pattern B needs an explicit counter in the agent loop.

All explicitly later — they matter once the two-tier path, governance gate, and
skill layer land.

## Frontend — ops console (not a reviewer P-item; foundational gap)

**Status (v1 scaffolded, 2026-06-04):** `src/frontend/` — Vite + React + TypeScript
+ react-query. **Ask** panel (POST `/api/query` → answer, status, confidence,
sources, trace_id) and a **Pods** table (GET `/admin/pods`), wired to the backend
via Vite's dev proxy. **Validated by running (2026-06-04):** Vite serves, the Pods
table polls `/admin/pods` (200), and an Ask query rendered the full `QueryResponse`
including the `trace_id` (provenance visible in the UI). Full `tsc` typecheck
(`npm run typecheck`) recommended as the last check. **Deferred:** shadcn/Tailwind
(CLAUDE.md stack) until iterated visually; admin integrations CRUD (backend handlers
are stubs); WebSocket chat (backend TODO).
