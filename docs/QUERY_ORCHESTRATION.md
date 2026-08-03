# Query Orchestration

> Rewritten 2026-08-03 — the previous version described lowercase, unexported
> method names (`routeToPods()`/`fetchFromPod()`) that don't match the real,
> exported API, predated the two-tier query path
> ([ADR 0006](../context-mesh/decisions/0006-two-tier-query-path.md)), and
> predated cluster-aware routing
> ([ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md)).

## Overview

**Everything in this file runs on the Hub and never contacts Discovery.**
`cluster_id` here only selects *which rows of the Hub's own Postgres store*
to read (via a cluster-scoped pod ID) — it is not a live call out to that
cluster. For the operations that *do* reach into a fleet cluster in real
time, see `docs/AGENT_INTEGRATION.md`'s "Fleet clusters & live drill-down"
section and `SEQUENCE_FLOWS.md` diagram 6 instead — those go through
`CollectorHub`/`POST /api/live-fetch`, a completely separate code path from
`QueryExecutor`.

Two distinct entry points route through `QueryExecutor`, for different
purposes:

1. **`Execute()`** — used by `HandleQuery` (`POST /api/query`)'s initial
   Tier-1/Tier-2 routing. Not cluster-scoped (no `cluster_id` concept exists
   at this stage of the flow yet).
2. **`RouteToPods()` + `FetchFromPod()`** directly — used by
   `HandleAgentFetch` (`POST /api/agent/fetch`), the per-tool-call path the
   Python agent's skills use. **This one accepts an optional `cluster_id`**
   (ADR 0024) so a skill that has resolved a fleet cluster
   (`resolve_service_clusters`, ADR 0023) can read that cluster's data
   specifically.

```
User query ("Is service X healthy?")
  ├─ Parse intent ("health_check")
  ├─ RouteToPods(intent, namespace, clusterID="") — Execute() never has a clusterID
  ├─ FetchFromPod for each pod
  ├─ Correlate results (if multi-pod)
  └─ Return data + sources

Agent tool call (get_service_health {..., cluster_id: "cluster-42"})
  ├─ HandleAgentFetch extracts cluster_id from the tool args
  ├─ RouteToPods(intent, namespace, clusterID="cluster-42")
  │    └─ builds a cluster-scoped pod ID (models.PodID) instead of the plain one
  └─ FetchFromPod → cluster-42's rows specifically
```

## Components

### Query Executor (`src/backend/internal/orchestrator/query_executor.go`)

```go
func (qe *QueryExecutor) Execute(ctx, intent string, query map[string]interface{}, namespace string) (map[string]interface{}, error)
func (qe *QueryExecutor) RouteToPods(ctx, intent, namespace, clusterID string) ([]*models.Pod, error)
func (qe *QueryExecutor) FetchFromPod(ctx, pod *models.Pod, query map[string]interface{}) ([]map[string]interface{}, error)
```

`RouteToPods`' switch, keyed on `intent`:

| Intent | Routing | Cluster-aware? |
|---|---|---|
| `health_check` | `routeLiveState(namespace, clusterID)` — namespace-sharded, falls back to a fan-out across all `k8fy.live-state.*` shards if the specific shard doesn't exist | ✅ `models.PodID("k8fy.live-state", clusterID, namespace)` |
| `metrics_query`, `metrics_history` | `getLeafPod("k8fy.metrics")` | ✅ `models.PodID("k8fy.metrics", clusterID)` |
| `cert_check` | `getLeafPod("k8fy.certificates")` | ✅ `models.PodID("k8fy.certificates", clusterID)` |
| `change_history` | `getLeafPod("k8fy.events")` | ✅ `models.PodID("k8fy.events", clusterID)` |
| `diagnose` / default | every active `k8fy` leaf pod (fan-out, `ListPods` + `leavesOnly`) | ❌ not cluster-scoped — reached via `Execute()`'s initial routing, not `HandleAgentFetch`'s per-tool path |

An empty `clusterID` reproduces today's exact, unscoped pod IDs — this is
what keeps every existing single-cluster deployment byte-for-byte unchanged.

### `HandleAgentFetch` (`src/backend/internal/api/handlers.go`)

```go
clusterID := stringArg(req.Args, "cluster_id")  // optional, ADR 0024 — not a bearer credential
pods, err := h.queryExec.RouteToPods(r.Context(), intent, namespace, clusterID)
```

`mapToolToQuery(tool, args)` still derives `(intent, namespace, key)` from
the tool name/args exactly as before; `cluster_id` is read separately,
directly from `args`, and threaded through unchanged.

## Usage

```go
executor := orchestrator.NewQueryExecutor(registry, backendFactory, logger)

// Tier-1/Tier-2 initial routing (HandleQuery) — no cluster scoping.
result, err := executor.Execute(ctx, "health_check", query, "prod")

// Per-tool-call routing (HandleAgentFetch) — optionally cluster-scoped.
pods, err := executor.RouteToPods(ctx, "health_check", "prod", "cluster-42")
for _, pod := range pods {
    rows, _ := executor.FetchFromPod(ctx, pod, query)
}
```

## Files

- `src/backend/internal/orchestrator/query_executor.go` — routing + fetch + correlate
- `src/backend/internal/orchestrator/query_executor_test.go` — cluster-scoping regression tests
- `src/backend/internal/api/handlers.go` — `HandleQuery`, `HandleAgentFetch`, `mapToolToQuery`
- `src/backend/internal/models/shard.go` — `models.PodID`, the cluster-aware pod-ID builder both routing paths that support `clusterID` use
