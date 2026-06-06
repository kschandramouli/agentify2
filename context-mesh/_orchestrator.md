# Orchestrator — the primary pod

> The orchestrator is the "front door" of the runtime context-mesh. It receives a
> user query, decides which pod(s) can answer it, fans out if needed, correlates
> results, and returns an answer. This file is the **human/Claude-readable
> specification** of that behavior. The live pod map itself is the
> `pod-registry` (a runtime artifact the system maintains, not this file).

## Responsibilities

1. **Interpret** the incoming query (intent, entities, time range, freshness needs).
2. **Route** to the right pod(s) using the pod-registry + the policies.
3. **Fan out & correlate** when no single pod suffices (see [correlation](policies/correlation.md)).
4. **Rank / merge / resolve conflicts** across pod responses.
5. **Emit feedback** (hits, misses, latency) into the [refinement-loop](policies/refinement-loop.md).

## Routing decision

> Day-one router is rules-based; it can be made learned later. The routing is
> **two-tier** — see [ADR 0006](decisions/0006-two-tier-query-path.md).

**Tier selection (first decision).** The orchestrator classifies the query intent,
then picks a tier:

- **Tier 1 — deterministic fast-path:** intents with a known rule and sufficient
  data (`health_check`, `cert_check`, threshold `metrics_query`) are answered
  directly from the store via the pure-function evaluators — **no LLM call**.
- **Tier 2 — agentic path:** free-text, compound, ambiguous, or causal questions —
  and any Tier-1 case whose rule is inconclusive — go to the LLM with tools. Tier 1
  **falls through** to Tier 2 rather than returning a weak answer.

**Within a tier — pod routing:**

- **Inputs available at routing time:** query text, parsed intent, the requested
  K8s namespace (from context), and the pod-registry.
- **How is a candidate pod scored?** By the K8fy taxonomy (ADR 0005): intent maps
  to a pod family, the namespace selects the shard (`k8fy.live-state.<ns>`). Index
  pods are never fetched (no data). _Vector-similarity scoring against pod summaries
  is future, gated on the storage-consolidation work in [ROADMAP](ROADMAP.md)._
- **Single-pod vs multi-pod (fan-out):** a query scoped to one namespace hits one
  shard; an unscoped or cross-namespace query fans out across the family's shards
  and the results are correlated (see [correlation](policies/correlation.md)).
- **Tie-breaking / ranking across pods:** for the MVP, all matched leaf shards are
  returned and merged; source authority/recency ranking is future.
- **Fallback when no pod matches:** return an explicit "no data available" rather
  than fabricating; pod *creation* happens on ingest (pod-formation), not on query.

## The pod-registry (runtime contract)

The registry is what the orchestrator reads to know what exists. Proposed shape
(refine in an ADR before locking it):

```jsonc
{
  "pods": [
    {
      "id": "pod_abc123",
      "kind": "leaf",                            // leaf (holds data) | index (holds a shard map)
      "summary": "<one line: what this pod owns>",
      "tags": ["billing", "invoices"],          // for routing
      "store_type": "vector | relational | kv | graph | document | timeseries | log_search | passthrough",
      "authority": "system-of-record | derived", // derived = a queryable mirror/index, not the master
      "schema_ref": "<pointer to schema, if structured>",
      "freshness": "2026-05-29T10:00:00Z",        // last updated
      "event_count": 12000,
      "query_stats": { "hits": 340, "misses": 12, "p95_latency_ms": 80 },
      "lifecycle": "active | merging | draining | retired"
    },
    {
      "id": "pod_k8fy_live_state",
      "kind": "index",                            // an index pod holds NO data — only a shard map
      "summary": "live K8s health, sharded by namespace",
      "store_type": "passthrough",                // leaves are live sources, not data at rest
      "authority": "derived",
      "partition_key": "namespace",               // the dimension shards are split on
      "shards": [
        { "child_id": "pod_ls_ns_payments", "partition": "payments", "event_count": 4200 },
        { "child_id": "pod_ls_ns_orders",   "partition": "orders",   "event_count": 9100 }
      ],
      "lifecycle": "active"
    }
  ],
  "updated_at": "2026-05-29T10:00:00Z"
}
```

> `kind: index` pods route to their `shards` (data at rest) or to live sources
> (for `passthrough`). A query crossing shards triggers fan-out + correlate. See
> [pod-formation](policies/pod-formation.md#index-pods--sub-pods-hierarchical--recursive-mesh).

## Open questions

- [ ] Is routing synchronous (block until best pod answers) or speculative (fan out, take first good answer)?
- [ ] How does the orchestrator know a pod's answer is *good enough* to stop?
- [ ] Where does the orchestrator itself live — same process as pods, or a separate service?
- [x] How is the pod-registry kept consistent / resilient? A read-through snapshot
      cache fronts the DynamoDB registry ([ADR 0012](decisions/0012-pod-registry-cache.md)):
      TTL refresh, serve-stale on error, invalidate on pod formation. (DynamoDB's
      managed multi-AZ handles durability; the cache handles hot-path load + transient blips.)
