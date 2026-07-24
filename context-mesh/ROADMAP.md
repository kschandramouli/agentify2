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
| **P6** | HashiCorp Vault integration — cert management + autonomous rotation | **✅ Scaffold done (2026-06-17)** — open items: Vault HA, Terraform provider, dynamic secrets |
| **P7** | **Eval harness as CI gate** — Langfuse dataset + CI eval step | **✅ Done (2026-06-25)** — `scripts/seed_eval_dataset.py` + `scripts/run_evals.py` + 02-deploy.yml gate; `intent`+`tier` added to QueryResponse | [ADR 0019](decisions/0019-eval-harness-as-ci-gate.md) |
| **P8** | RAG + pgvector + semantic memory (third memory layer) | After P7 | [ADR 0018](decisions/0018-three-layer-memory-architecture.md) |
| **P9** | PR review agent — second domain use case proving two-tier generalises | **Not started. Architecture decision (2026-07-20): build as its own deployable agent, not a `SkillRouter` entry in `src/agent`** — see below | — |
| **P10** | Context management at scale — budget-aware truncation, summarisation | Alongside P9 | — |
| **P11** | Multi-provider routing: Bedrock stub | After P9 | [ADR 0008](decisions/0008-multi-provider-model-routing.md) |
| **P12** | Multi-turn conversational chat — dedicated Chat nav page | After P11 | Architecture decided 2026-06-17 |
| **P13** | Agentic use cases expansion | **Use Cases 1+2 done (2026-07-20)** — see below; 3/4/5 not started | [spec 011](specs/011-agentic-use-cases.md), [ADR 0020](decisions/0020-phase-3-remediation-with-approval-gate.md) |
| **P14** | Split out two standalone agents: remediation executor (security isolation) + PR review agent (second domain) | **Next up (agreed 2026-07-20)** — see below | — |
| **P15** | Pull-based log-platform connector (Splunk first, Elasticsearch/OpenSearch second) — replaces direct-cluster log fetch with a query-time read against wherever logs already land | Test harness (Fargate+Firehose+S3/Athena) built 2026-07-21/22 — connector code itself not started | [spec 008](specs/008-on-demand-pod-logs.md), [ADR 0014](decisions/0014-on-demand-ephemeral-log-fetch.md) (extends, does not revisit), [ADR 0021](decisions/0021-log-platform-test-infra.md) |
| **P16** | Multi-cluster connector — wire the existing `Integration` model into runtime routing (currently admin-only bookkeeping) | Proposed (2026-07-21) — see below | `internal/models/integration.go`, `internal/api/adapter_client.go` |

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

### Langfuse prompt management ✅ Done (2026-06-11)

All six K8fy skill prompts are now managed via Langfuse under the label
`"production"` (names: `k8fy/system`, `k8fy/health-check`, `k8fy/cert-audit`,
`k8fy/change-history`, `k8fy/restart-trend`, `k8fy/diagnose`). The agent fetches
live prompts at startup via `k8fy/prompt_manager.py` with a local fallback so
the service starts cleanly without credentials. Prompts can now be edited in the
Langfuse UI and picked up without a code deploy (60 s cache TTL).

Setup: set `LANGFUSE_PUBLIC_KEY`, `LANGFUSE_SECRET_KEY`, `LANGFUSE_BASE_URL` in
environment or `.env`, then run `python scripts/migrate_prompts_to_langfuse.py`
once to push local strings into Langfuse.

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

## P6 — HashiCorp Vault Integration (cert management + autonomous rotation)

**Context (2026-06-17):** Development/test scaffold implemented to mimic the client
setup where TLS certificates are issued and rotated by HashiCorp Vault PKI rather
than cert-manager or manual Kubernetes secrets. This demonstrates how agentify can
act as an autonomous cert management agent for Vault-backed workloads.

### What was implemented

| Component | Location | Purpose |
|-----------|----------|---------|
| Vault Helm values | `infra/kubernetes/vault/vault-values.yaml` | Standalone Vault on EKS (dev/test) |
| Vault setup script | `scripts/vault-setup.sh` | Init, unseal, K8s auth, PKI engine, policies, roles |
| Payment service manifest | `infra/kubernetes/payments-test/payment-service.yaml` | nginx HTTPS with Vault Agent Injector annotations — cert injected from Vault PKI at runtime |
| Cert rotator CronJob | `infra/kubernetes/vault/cert-rotator-cronjob.yaml` | Daily check: renews cert via Vault PKI if < 30 days remaining |
| VaultCertSkill | `src/agent/k8fy/skills/vault_cert.py` | Pattern A skill: pre-fetches Vault cert status + K8s cert status, one Claude call to assess and optionally call `rotate_vault_cert` |
| Vault tools | `src/agent/k8fy/tools.py` | `get_vault_cert_status` + `rotate_vault_cert` — call Vault HTTP API directly from the agent |
| `vault_cert` intent | `inferIntent()` in `handlers.go` | Queries containing "vault", "pki", "rotate cert", etc. route to VaultCertSkill |

### Architecture

```
HashiCorp Vault (helm, standalone)
├── PKI engine  → issues TLS certs for payment.payments.svc.cluster.local
├── KV v2       → stores renewed certs for audit
└── K8s auth    → payment-service + cert-rotator ServiceAccounts

payment-service pod
└── Vault Agent Injector sidecar
    ├── On start:  fetches cert from PKI → writes /vault/secrets/tls.crt
    └── On renew:  detects lease expiry → fetches new cert → SIGHUP nginx

cert-rotator CronJob (daily)
└── Checks cert expiry via Vault KV
    └── If < 30 days: vault write pki/issue/payment-service

agentify Investigate / K8s Observability
└── "Is the Vault cert for payment-service healthy?"
    → vault_cert intent → VaultCertSkill
    → get_vault_cert_status tool (queries Vault HTTP API)
    → Claude: assess + optionally call rotate_vault_cert
    → "Cert expires in 12 days — rotating now..."
    → rotate_vault_cert tool → new cert issued + stored in KV
```

### Open items (deferred)

- **Production Vault HA** — Raft storage, multi-node, TLS between Vault nodes
- **Vault Agent template updates** — auto-restart pods (not just SIGHUP) on cert change
- **Terraform Vault provider** — manage PKI roles and policies as code
- **Vault Enterprise** — namespace isolation per tenant (aligns with P3a)
- **Dynamic secrets** — extend VaultCertSkill to manage DB credentials, not just TLS
- **Audit log integration** — stream Vault audit logs into agentify event store for anomaly detection

---

## Post-Vault Gap Analysis (2026-06-17)

> Items P7–P12 were identified through a structured technical review against
> senior LLM-engineer evaluation criteria. They are ordered by impact on demonstrable
> architectural depth, not by implementation complexity. P7 (eval harness) must ship
> first — it gates credibility of everything else.

---

## P7 — Eval Harness as CI Gate ⚡ Immediate priority

**Hard truth:** The eval harness has been listed as P5+ since day one and is the
most damaging gap in the portfolio. The feedback explicitly praised evaluation pipeline
work. Agentify has no eval code — only a roadmap line. This must be fixed first.

**Prerequisite met:** All five skills are on Pattern A (ADR 0017). The infrastructure
is in place (Langfuse wired, `trace_id` returned, `query.trace` logged). The missing
piece is the test dataset and CI step.

**Acceptance criteria:**
- Langfuse dataset `k8fy-regression` with ≥ 10 (query, ground-truth) pairs covering
  all intent classes
- `scripts/run_evals.py` — POSTs each query to `/api/query`, scores against ground
  truth (intent, tier, status, required fields, latency), records score against
  `trace_id` in Langfuse
- CI step in `02-deploy.yml` that runs the eval post-rollout and blocks on score < 0.85
- Scores visible in Langfuse UI alongside production traces

**Architecture:** See [ADR 0019](decisions/0019-eval-harness-as-ci-gate.md).

---

## P8 — RAG + Semantic Memory (pgvector)

**Hard truth:** RAG is explicitly listed as a required production LLM pattern in
evaluation criteria. Agentify deferred pgvector as YAGNI (ADR 0010). That decision
was correct at the time; it is no longer correct.

**What it does:** Embeds diagnostic outputs at trace-persist time. When a `diagnose`
query fires, the pre-fetch sequence retrieves the top-3 semantically similar past
incidents and injects their summaries into the Claude call context. The system learns
from its own history — a second incident with the same root cause gets a higher-
confidence diagnosis faster.

**Architecture:**
```
Trace persisted (Tier-2 answer stored)
  → async embed(headline + findings + likely_cause) via Haiku
  → INSERT INTO incident_embeddings (trace_id, embedding, summary, ...)

DiagnoseSkill._prefetch() [Pattern A, new signal]
  get_similar_incidents(service, namespace, description)
    → SELECT ... ORDER BY embedding <-> $query_vec LIMIT 3

Claude sees: "Similar past incidents: [date, likely_cause, what resolved it]"
```

**Implementation steps:**
1. Enable `pgvector` extension on RDS (`CREATE EXTENSION IF NOT EXISTS vector`)
2. Add `incident_embeddings` table (migration in `initSchema`)
3. Async embed goroutine in `logTrace` — calls a new `/embed` endpoint on the agent
4. New `get_similar_incidents` tool in `tools.py`
5. Add tool to `DiagnoseSkill._prefetch()` pre-fetch sequence
6. Add `DIAGNOSE_REASONING_SCHEMA` field for `similar_incidents` context

**See also:** [ADR 0018](decisions/0018-three-layer-memory-architecture.md) — formal
definition of the three-layer memory model this completes.

---

## P8b — Memory Architecture: Reframe and Document

**The reframe:** agentify already has two of the three memory layers. The third
(semantic) is what P8 adds. Once P8 ships, the full architecture is:

| Layer | What it is | Where in agentify |
|-------|-----------|-------------------|
| **Working memory** | In-request context; K8s signals pre-fetched by Pattern A | Pattern A skill pre-fetch; multi-turn session cache (P12) |
| **Episodic memory** | Time-ordered append-only event history | `events` table: `k8fy.metrics`, `k8fy.events`, `k8fy.certs` |
| **Semantic memory** | Vector retrieval over past incident knowledge | `incident_embeddings` + pgvector (P8) |

**Action:** After P8 ships, update the architecture documentation and any public-
facing descriptions to lead with this three-layer framing. It is a defensible and
demonstrable architectural pattern — not just a feature list.

---

## P9 — PR Review Agent (second domain use case)

**Hard truth:** The JD lists PR review as the primary use case. Agentify is K8s
observability only. Without at least one demonstrable artifact outside K8s, the
portfolio reads as narrow.

**Architectural conviction:** The same two-tier pattern generalises. PR review is
not a different architecture — it is the same architecture on a different domain:

```
Tier-1 (deterministic):
  - File count delta
  - Test coverage delta (from CI metadata)
  - Dependency changes (package-lock.json / go.sum diff)
  - Binary/generated file changes
  → Returns structured flags: [{severity, file, reason}]

Tier-2 (LLM, only if Tier-1 finds issues or query requests deep review):
  - CodeReviewSkill (Pattern A)
  - Pre-fetch: diff, related test files, historical PR patterns
  - One Claude call → structured findings [{file, line, severity, explanation, suggestion}]
```

**Why it matters beyond the interview:** This proves the architectural thesis of agentify
is generalisable infrastructure, not a single-purpose K8s tool. It is the foundation
for positioning agentify as a "developer intelligence platform" vs a K8s monitoring tool.

**MVP scope:**
- GitHub webhook receiver (or on-demand `POST /api/review { repo, pr_number }`)
- `inferIntent` extended to recognise `pr_review` intent
- `PRReviewSkill` following Pattern A (fetch diff + test delta → one Claude call)
- `PRReviewCard` component in the frontend (same structure as `DiagnosisCard`)

---

## P10 — Context Management at Scale

**Hard truth:** Pattern A is a cost optimisation (deterministic pre-fetch → one call).
It is not a context management strategy. At scale, the signals it injects (full logs,
full event history, full restart metrics) can easily fill a 50k-token context. The
system has no budget-aware truncation, no hierarchical summarisation, no selective
retrieval. This was identified as a specific gap in the technical review.

**What needs to be added:**

1. **Context budget per skill** — each Pattern A skill has a `MAX_CONTEXT_TOKENS`
   constant. If pre-fetched data exceeds the budget, truncate deterministically
   (most-recent events first; truncate logs to last N lines; drop metrics beyond
   a configurable window). Log the truncation so it is visible in traces.

2. **Hierarchical summarisation trigger** — for multi-turn chat sessions (P12):
   after 20 turns, automatically summarise the early history into a compact block
   that replaces it. This is the same pattern as `compact_20260112` in the Anthropic
   API — implement at the application level as a fallback.

3. **Budget-aware tool selection** — in the `_reason_single` agentic path (general
   queries), track remaining context budget after each tool call. Stop calling tools
   when `budget_remaining < MIN_SYNTHESIS_TOKENS`. This converts Pattern A's static
   pre-fetch into a dynamic version.

**Framing for technical interviews:** "Pattern A enforces a deterministic context
budget — we pre-fetch exactly the signals we need and nothing more. The agentic path
adds budget tracking to prevent runaway context growth. Summarisation is triggered at
session boundaries, not per-call."

---

## P11 — Multi-Provider Routing: Bedrock Stub

**Hard truth:** ADR 0008 deferred Bedrock/Vertex until a client requires it. That is
the correct production decision. For demonstrability, the implementation is missing.
Evaluators ask "have you actually implemented provider switching?" — "yes, the
architecture supports it" is a weaker answer than "yes, here's the Bedrock client."

**Minimum viable implementation:**
- Add `AnthropicBedrock` client path in `config/claude_client.py`
  (uses `anthropic.AnthropicBedrock(region_name=...)`)
- Wire via `CLAUDE_PROVIDER=bedrock` env var
- Test that one skill (HealthSkill) works end-to-end on Bedrock
- Document the model ID change: `claude-opus-4-8` → `anthropic.claude-opus-4-8`

This is a single file change + one integration test. The architecture is already
designed (ADR 0008). Execution is all that is missing.

**See also:** [ADR 0008](decisions/0008-multi-provider-model-routing.md) — full
provider routing design. The stub activates the `BEDROCK` branch of that ADR.

---

## P12 — Multi-Turn Conversational Chat (Dedicated Chat Page)

**Decision:** Implement as a **dedicated admin nav item** ("Chat"), not integrated
into the existing K8s Observability ServiceEvaluator flow. Rationale: clean separation
of interaction paradigms; supports open-ended questions not tied to a specific service;
simpler to build and test independently.

**Architecture decisions confirmed (2026-06-17):**

| Concern | Decision | Rationale |
|---------|----------|-----------|
| Transport | HTTP POST (send turn) + SSE (receive stream) | Simpler than WebSocket; works through ALB; browser auto-reconnects SSE |
| Session state | Postgres `chat_sessions` + Go `sync.Map` write-through cache | Survives pod restarts; any pod can serve a session |
| K8s context | Cache K8s signals in session (5-min TTL), explicit refresh on demand | Avoids re-fetching on every turn; "show me the latest data" triggers refresh |
| Tier routing | Multi-turn always Tier-2; Tier-1 data seeded as opening context | Tier-1 is single-shot by design; conversation is inherently Tier-2 |
| Context window | Full history + prompt caching; summarise at 20 turns | Cache makes marginal per-turn cost small; summarisation prevents runaway cost |
| Frontend | New `ChatPanel` component + `ChatNavItem` in admin sidebar | Dedicated page with message thread, streaming tokens, typing indicator |

**New endpoints:**
```
POST /api/chat/sessions          → create session { session_id }
POST /api/chat/{id}/messages     → send user turn { message_id }
GET  /api/chat/{id}/stream       → SSE: token stream for current turn
GET  /api/chat/{id}/history      → full conversation history
DELETE /api/chat/{id}            → close session
```

**New Postgres table:**
```sql
CREATE TABLE chat_sessions (
    id                 TEXT PRIMARY KEY,
    namespace          TEXT NOT NULL DEFAULT '',
    service            TEXT NOT NULL DEFAULT '',
    summary            TEXT NOT NULL DEFAULT '',  -- summarised old history
    messages           JSONB NOT NULL DEFAULT '[]',
    context_cache      JSONB NOT NULL DEFAULT '{}',
    context_fetched_at TIMESTAMP,
    created_at         TIMESTAMP DEFAULT NOW(),
    last_active        TIMESTAMP DEFAULT NOW(),
    expires_at         TIMESTAMP
);
```

**Implementation stages (do not skip stages):**

| Stage | What | Validation |
|-------|------|-----------|
| 1 | Session CRUD (Postgres table + Go endpoints) | `POST /sessions` returns 200, `GET /history` returns empty array |
| 2 | Non-streaming multi-turn (full response, no SSE) | Conversation works end-to-end; history grows correctly |
| 3 | Streaming (SSE from Python agent → Go → frontend) | Tokens appear progressively in ChatPanel |
| 4 | K8s context cache + Tier-1 seed on session start | Session opens with pod health pre-loaded |
| 5 | Summarisation at 20 turns | Long sessions compress without losing context |

**See also:** Architecture discussion in project conversation history (2026-06-17).

---

## P13 — Agentic use cases: Incident Responder + Deployment Guardian (2026-07-20)

**Status: done, with a deliberate governance change from spec 011's original design.**
Use Case 1 (Incident Responder) and Use Case 2 (Deployment Guardian) are built,
gated by a mandatory human-approval step for every write action — see
[ADR 0020](decisions/0020-phase-3-remediation-with-approval-gate.md), which
amends [ADR 0003](decisions/0003-read-only-to-actions-boundary.md) to authorize
Phase-3 actions (restart/scale/rollback) with that gate. Spec 011's original
"confidence > 0.9 → auto-rollback" idea for Use Case 2 was explicitly rejected —
every proposal requires an explicit approve/reject, regardless of confidence.

**What shipped:**
- `remediation_proposals` Postgres table + propose→approve/reject→execute
  lifecycle (idempotent decisions, TTL-bounded proposals).
- `IncidentResponderSkill` (wired into the existing spec 009 investigation
  loop) and `DeploymentGuardianSkill` (new in-process poller over `k8fy.events`
  deploy rows) — both Pattern A, both propose-only, never execute.
- `action_executor.py` — the only code path that writes to a K8s Deployment
  (restart/scale/rollback-via-change-history), reachable exclusively through a
  deterministic `execute_remediation` dispatch after approval; never exposed
  as a Claude-callable tool.
- Admin Console **Remediation** panel (Approve/Reject) backed by the same POST
  API (`/admin/remediation/{id}/approve|reject`, bearer-token guarded) so an
  external approver (Slack, PagerDuty) can call it later without a redesign.
- RBAC: a new namespace-scoped `agent-remediator` Role (mirrors the existing
  `agent-cert-renewer` pattern), not a namespace-wide grant.

**Deliberately deferred:** full ReplicaSet-revision rollback (MVP replays the
previous deploy event's recorded images instead); Use Cases 3 (Capacity
Intelligence), 4 (Knowledge Builder), 5 (PR Review) — untouched by this pass.

---

## P14 — Split out two standalone agents (agreed 2026-07-20)

**Context:** everything in `src/agent` today is one FastAPI process with nine
`SkillRouter` skills plus the chat agent — a router pattern (deterministic
Go-side `inferIntent()` dispatch, not agent-to-agent delegation; see the
"multi-agent vs one-agent-multi-skill" discussion, 2026-07-20). Reviewed
whether any current or planned skill has a strong enough reason to be pulled
out into its own deployable agent instead. Most don't — they share the same
data model, the same read-only boundary, and the same redaction/tracing/cost
plumbing, so splitting them would just reintroduce the polyglot-microservice
complexity [ADR 0010](decisions/0010-postgres-single-store.md) already paid
down once. Two do:

### P14a — Remediation executor as its own network-isolated agent

**Why:** `RemediationExecutorSkill` / `action_executor.py` (P13 / [ADR
0020](decisions/0020-phase-3-remediation-with-approval-gate.md)) is the only
write-capable code in the whole system. Today its isolation from the LLM
reasoning loop is enforced **by convention** — it's simply never registered
as a Claude-callable tool. That's a code-review guarantee, not an
infrastructure one. Splitting it into its own minimal service converts that
into a network-level guarantee: the general reasoning agent (which processes
untrusted-ish inputs — log lines, adapter data) would have **no network path**
to the K8s write RBAC at all, even if a future, more agentic skill widened the
model's tool surface. Least-privilege separation between "reasons over data"
and "holds write credentials" is a standard security boundary, not a
theoretical one.

**Shape:** a small stateless service exposing one dispatch endpoint
(`restart_deployment` / `scale_deployment` / `rollback_deployment`), called
only by the backend's `/admin/remediation/{id}/approve` path after a human
has approved a proposal — same trigger, same RBAC (`agent-remediator` Role),
just moved out of the general agent pod. No LLM in this service at all.

### P14b — PR review agent (Use Case 5 / P9) as its own agent from the start

**Why:** spec 011 already frames this as "the second-domain use case that
proves the two-tier pattern generalises" — it shares zero data model with K8s
pods/certs/events, needs entirely different tools (PR diff, GitHub API) and
credentials (GitHub tokens, not K8s/Vault), and is triggered by a different
event source (GitHub webhook) with no dependency on the K8fy pipeline
running. Folding it into `SkillRouter` as a tenth entry would be the wrong
call for a domain this disjoint — build it as its own deployable agent (or at
minimum a fully standalone module) from day one, reusing the two-tier
pattern (deterministic Tier-1 lint checks + Pattern-A Tier-2 skill) but not
the K8fy process.

**Sequencing:** independent of each other; P14a is the higher-priority of the
two (closes an actual security gap in shipped code), P14b can land whenever
P9 is picked up.

---

## P15 — Pull-based log-platform connector (proposed 2026-07-21)

**Context:** today's `get_pod_logs` fetches a bounded tail directly from the K8s
API via the adapter, redacts it (denylist scrubber — freeform text can't use
the allowlist gate structured fields get), and discards it — never persisted
(spec 008, [ADR 0014](decisions/0014-on-demand-ephemeral-log-fetch.md)). The
ask: stop hitting cluster logs directly; integrate with wherever the org's
logs actually land (Splunk, an OpenSearch/Elasticsearch index, potentially fed
by Kinesis Firehose).

**Decision (2026-07-21):** pull, not push. The agent becomes a bounded,
time-windowed *reader* of the existing log platform at diagnosis time — same
fetch-redact-discard discipline as today, just a different backend behind the
same tool shape. This explicitly does **not** revisit ADR 0014 or ADR 0010
(Postgres-only): nothing new gets persisted, so the "logs are ephemeral, no
retention question" premise holds. **Rejected alternative:** continuous
stream consumption (agentify itself consuming a live Kafka/Kinesis topic and
indexing it) — bigger lift (needs a real log store, volume-aware retention,
ingest-time structured redaction instead of the denylist scrubber), and only
justified by a proactive/pattern-mining use case that doesn't exist yet. ADR
0014's own "revisit if" clause anticipates that path; treat it as a distinct
future decision if a concrete use case (e.g. extending the P4c investigation
loop) demands it — don't bundle it into this item.

**Connector priority (confirmed 2026-07-22): Splunk first, Elasticsearch/
OpenSearch second.** Splunk is its own implementation (SPL via the REST
search-jobs API, Splunk token auth). Elasticsearch and OpenSearch share
close enough to the same `_search` Query DSL that one connector covers both —
the design below (query construction, schema) was written against that
shared API and still applies once it's built as the second connector.

**Shape:** a pluggable log-backend abstraction — direct K8s fetch (existing)
becomes one implementation; OpenSearch becomes a second. Both sit behind the
same `get_pod_logs`-equivalent tool contract (bounded tail/window, entity
filter, redact-before-the-agent-sees-it). No new Postgres tables, no new
retention janitor.

**Design refined 2026-07-21 (brainstorm), scope confirmed as read-connector
only — the ingest pipeline (Fluent Bit/Firehose + OpenSearch domain + index
templates) is explicitly out of scope for this item; assumed to exist or be
stood up separately:**

- **Clarified Firehose's role:** it's a delivery mechanism (producer →
  buffer/batch → OpenSearch), not something queried at read time. Two
  standard ingest topologies exist upstream of this item — Fluent Bit → ES
  output plugin directly, or Fluent Bit/app → CloudWatch Logs → subscription
  filter → Firehose → OpenSearch (only worth the extra hop for fan-out to a
  second destination like S3). Neither is this item's concern; only the
  resulting OpenSearch index/schema is.
- **Interface:** a `LogSource` Go interface (`FetchLogs(ctx, LogQuery) (LogResult, error)`)
  behind the existing `handlePodLogs` call site. `K8sAdapterLogSource` wraps
  today's `AdapterClient.FetchLogs` unchanged; `OpenSearchLogSource` is the
  new implementation. Selected by config (`LOG_SOURCE=k8s_adapter|opensearch`).
  The agent-side `get_pod_logs` tool contract (`LogRequest`/`LogResponse` shape
  in `internal/api/adapter_client.go`) does not change.
- **Query construction:** OpenSearch Query DSL (`POST /<index>/_search`) — bool
  query on namespace + pod (prefix match for the deployment, same K8s-hash-
  suffix-stripping already used elsewhere) + container; mandatory bounded
  `range` filter on `@timestamp` (never an open-ended query); `sort` desc;
  `size` capped to match today's 100-default/200-cap tail convention.
  `previous=true` (crashed-container-instance logs) has no direct OpenSearch
  equivalent without a restart/instance-id field on documents — fallback is
  bounding the time window tightly around the restart timestamp already known
  from `k8fy.metrics`, which `DiagnoseSkill` already correlates against.
- **Schema (greenfield — proposed, not yet built):** modeled on Fluent Bit's
  standard Kubernetes-filter output (near-zero-transform match if Fluent Bit
  ever becomes the actual shipper) rather than a bespoke convention:
  ```
  @timestamp                    — range-filter field
  kubernetes.cluster_name       — P16 forward-compat (unused until multi-cluster)
  kubernetes.namespace_name
  kubernetes.pod_name
  kubernetes.container_name
  kubernetes.labels.app         — optional, deployment-level correlation
  log.level                     — parsed level if the shipper extracts one
  message                       — freetext line; the only field the denylist scrubber touches
  stream                        — stdout/stderr
  ```
  Index naming: daily-rotated (`logs-<yyyy.MM.dd>`, queried via a `logs-*`
  alias) with retention handled by an OpenSearch **Index State Management
  (ISM)** policy (automatic rollover/deletion) — replaces the Postgres
  age-based janitor pattern (ADR 0015 already flags that janitor as a
  stopgap not meant for real log volume; ISM is the native mechanism for
  exactly this).
- **Two-layer redaction — a real improvement over today, not just a lateral
  move:** because OpenSearch documents are structured (unlike a raw K8s log
  tail), the connector can allowlist at the *field* level (only ever return
  `{timestamp, level, message, pod, namespace, container}`, silently dropping
  anything else — arbitrary custom labels, attached metadata) **and** still
  denylist-scrub the freetext `message` field with the existing `RedactText`
  scrubber. Belt and suspenders, not possible with a raw pod-log tail. Worth
  documenting as an ADR update when this lands, not just folded in silently.
- **Auth: IAM via IRSA** (SigV4-signed requests, fine-grained access control
  scoped to search/get only — no write/delete) — same pattern already used
  for Secrets-Manager-backed credentials elsewhere, avoiding a new static
  credential to rotate. Rejected: basic auth (master user/password) — only
  the fallback if the OpenSearch domain isn't using IAM-based access control.
- **Reliability:** request timeout + graceful degradation on a slow/failed
  query (same `process_tool_call` convention already used everywhere in
  `tools.py` — a failed tool call returns "logs unavailable," never crashes
  the diagnose call).

**Test harness built 2026-07-21/22 ([ADR 0021](decisions/0021-log-platform-test-infra.md)):**
Fargate profile (`payments` namespace) → Kinesis Firehose → **S3 (Hive-partitioned)
+ Athena** — not OpenSearch. This is scaffolding to validate the `LogSource`
interface cheaply (Athena has zero idle cost, unlike a continuously-billed
search-engine instance); it is explicitly not a production connector target —
real customers' source of truth is Splunk or Elasticsearch/OpenSearch, per the
priority above, and those connectors are what actually ship. See ADR 0021 for
the full infra design (Fargate/cluster-onboarding registry, Glue partition
projection, IRSA-based query access reusing the existing backend/agent roles).

## P16 — Multi-cluster connector (proposed 2026-07-21)

**Context:** the ask is to keep adding cluster connections (integration +
authn/authz) over time, not just one hardcoded adapter. This is **more built
than it looks**: `Integration` (`internal/models/integration.go`) already has
almost the right shape — `{ID, Name, AdapterURL, Namespaces, Status, Token}`,
one row per cluster/adapter, full CRUD, admin UI panel. What's missing is
wiring: the actual outbound adapter client (`h.adapterClient` in
`handlers.go`) is a single global built once at startup from one adapter
URL/token — it never consults `integrationStore`.

**Decision (2026-07-21):** tracked as its own item, separate from P15 (log
platforms). Making it real requires: (1) routing a query's namespace/service
to the right `Integration` row (namespace→cluster mapping — explicit on the
record or discovered via sync); (2) a per-Integration `AdapterClient`
(keyed/cached), not a startup-time singleton; (3) moving `Integration.Token`
off plaintext Postgres storage — already flagged in the code as a prototype
shortcut — onto a Secrets-Manager reference, fetched via IRSA the same way the
agent already fetches its Anthropic/Langfuse keys, since per-cluster/per-log-
platform credentials (Splunk API tokens, Kafka SASL, Kinesis IAM) are more
sensitive than the single dev-cluster bearer token this shortcut was written
for.

**Explicitly not in scope:** this is multi-**cluster**, not multi-**tenant** —
[ADR 0009](decisions/0009-tenancy-single-tenant-per-deployment.md) (single-
tenant per deployment, no `tenant_id`/RLS) stays as-is. If this ever needs to
serve multiple *customers* (not just multiple clusters for one operator),
that's a separate, larger decision revisiting ADR 0009 — don't let tenant-
isolation machinery creep in under cover of this item.

---

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
