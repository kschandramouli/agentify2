# 0018 – Three-Layer Memory Architecture

## Status

Accepted   ·   (date: 2026-06-17)

## Context

A technical review of agentify against senior LLM-engineer evaluation criteria
identified a gap: the system has **working memory** and **episodic memory** already
implemented, but **semantic memory** (vector retrieval over past incidents) is absent
and was not architecturally framed as a gap. The review noted that the absence of all
three layers being explicitly designed for is a credibility signal to interviewers
assessing depth in memory architectures.

Separately, the P2b decision ([ADR 0010](0010-postgres-single-store.md)) deferred
pgvector as "YAGNI" — correct at the time (no semantic search feature existed), but
that deferral now needs to be reversed now that the RAG/semantic-retrieval use case
has been identified as both a production-valuable feature and a demonstrable
architectural artifact.

**What already exists (reframed as memory layers):**

| Memory type | What agentify has | Where |
|-------------|------------------|-------|
| **Working memory** | Per-request in-process context; `current_state` table (latest pod state, latest-wins) | `k8fy.live-state.*` pods, Pattern A pre-fetch context |
| **Episodic memory** | Append-only time-ordered history of events, restart metrics, deploys, cert changes | `events` table (`k8fy.metrics`, `k8fy.events`, `k8fy.certs`), temporal spine |
| **Semantic memory** | **MISSING** — no vector store, no embedding pipeline, no semantic retrieval | Roadmap P8 |

The gap is the third layer. Adding it completes the architecture and enables retrieval-
augmented generation (RAG) over past incident knowledge.

## Decision

**Formally adopt a three-layer memory model as the architectural target:**

1. **Working memory** — in-request context: the pre-fetched K8s signals assembled by
   Pattern A skills before the Claude call. Budget-capped per skill class (see P10).
   Per-session context for multi-turn chat (P12) is an extension of working memory.

2. **Episodic memory** — time-ordered append-only event history in Postgres: restart
   metrics, pod events, deploy/change events, cert lifecycle events. Already built.
   Extended by the temporal spine (ADR 0013). Query interface: `get_metrics_history`,
   `get_change_history`, `get_pod_events`.

3. **Semantic memory** — pgvector embeddings store over past diagnostic outputs.
   When a `diagnose` query fires, retrieve the top-k semantically similar past
   incidents and inject their summaries as context. Implementation: embed the
   `DiagnosisCard` headline + likely_cause + findings at trace-persist time, store
   in a `incident_embeddings` table using `pgvector`. Retrieval: new tool
   `get_similar_incidents(service, namespace, description)` added to DiagnoseSkill's
   pre-fetch sequence.

**Architecture of the semantic layer:**

```
Trace persisted (POST /api/query → Tier-2 answer stored)
  → async: embed(headline + findings + likely_cause)
  → INSERT INTO incident_embeddings (trace_id, namespace, service, embedding, summary)

DiagnoseSkill._prefetch() [Pattern A]
  existing:  get_service_health, get_pod_events, get_metrics_history, get_change_history, get_pod_logs
  new:       get_similar_incidents(service, namespace, description)
               → SELECT ... ORDER BY embedding <-> $query_embedding LIMIT 3
               → returns: [{summary, date, likely_cause, resolution}]

Claude prompt sees: "Similar past incidents: [...]"
```

## Consequences

- **Positive:** All three memory layers are populated and retrievable. The system
  learns from its own history — a second incident with the same root cause gets a
  higher-confidence diagnosis faster. Closes the "memory architecture" credibility gap.
- **Positive:** RAG pattern is now demonstrable as a production feature, not a
  deferred item. Explicitly listed as a required LLM production pattern in evaluation
  criteria.
- **Negative / cost accepted:** Requires the `pgvector` Postgres extension (previously
  deferred in ADR 0010). On AWS RDS, pgvector is available as an extension — `CREATE
  EXTENSION IF NOT EXISTS vector`. No infrastructure change beyond enabling it.
  Embedding cost: one Haiku call per trace (cheapest model, ~100 tokens). Estimated
  $0.0001 per trace — negligible.
- **Negative / cost accepted:** Adds ~10ms to trace-persist path (async goroutine,
  does not block the query response). Schema migration adds `incident_embeddings` table.
- **Revisit if:** Incident volume grows to >1M embeddings — at that scale, consider
  moving to a dedicated vector DB (Qdrant, Weaviate) as planned in ADR 0010's scaling
  path.
