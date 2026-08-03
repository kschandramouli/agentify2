# Pod Registry

> Rewritten 2026-08-03 — the previous version's usage example called
> non-existent methods (`ListPodsByNamespace` was described with a
> different signature than the real one) and predated the multi-tenancy
> and cluster-scoping work (ADR 0022/0024). This version matches
> `registry.go`/`cache.go`/`pod.go` as they stand today.

## What is it?

**This entire file describes a Hub-only concept.** The pod registry lives
inside the Hub process (DynamoDB or in-memory, see below); Discovery
(`agentify-discovery`, the per-cluster fleet collector) never reads or
writes it directly — it only ever POSTs data to Hub endpoints
(`/api/cluster-inventory`, `/api/service-dependencies`), and it's the Hub's
own ingestion path that turns those pushes into registry entries. If you're
looking for what Discovery itself does, see `docs/AGENT_INTEGRATION.md`'s
"Fleet clusters & live drill-down" section instead.

The pod registry is the **runtime source of truth** for what pods exist in
the context-mesh — logical storage shards, not Kubernetes pods (see
[glossary.md](../context-mesh/glossary.md) if that distinction is new). It
stores:
- Pod metadata (ID, kind, storage backend, lifecycle)
- Pod configuration (partition key for sharding, index shard maps)
- Pod statistics (event count, query hit/miss/latency)
- Pod hierarchy (index pods hold a shard map over their child leaf pods, ADR 0002)
- Tenant/cluster metadata (`TenantID`/`ClusterID` — see "Multi-tenancy" below)

See:
- `context-mesh/policies/pod-formation.md` — lifecycle rules
- [ADR 0002](../context-mesh/decisions/0002-pods-are-recursive.md) — recursive pod design
- [ADR 0012](../context-mesh/decisions/0012-pod-registry-cache.md) — the read-through cache described below
- [ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md) — cluster-aware pod IDs

## Files

### Models
- `src/backend/internal/models/pod.go` — `Pod`, `ShardRef`, `QueryStats`, `PodFilter`, `PodStats`
- `src/backend/internal/models/shard.go` — `PodID(parts ...string) string`, the cluster-aware pod-ID builder (ADR 0024)

### Registry client
- `src/backend/internal/storage/registry/registry.go` — `Registry`, implementing the `PodStore` interface. Backed by DynamoDB when constructed with a real client; runs fully in-memory (a `sync.RWMutex`-guarded map) when constructed with `nil` — this is what local dev and every Go unit test in this repo actually use, no LocalStack/DynamoDB emulator needed.
- `src/backend/internal/storage/registry/cache.go` — `Cache`, a read-through snapshot wrapper over any `PodStore` (ADR 0012)

### `PodStore` interface

Both `Registry` and `Cache` implement this; callers (the orchestrator,
ingester) depend on the interface, never a concrete store:

```go
type PodStore interface {
    UpsertPod(ctx, pod *models.Pod) error
    GetPod(ctx, podID string) (*models.Pod, error)
    ListPods(ctx, filter *models.PodFilter) ([]*models.Pod, error)
    ListPodsByNamespace(ctx, namespace string) ([]*models.Pod, error)
    ListActivePods(ctx) ([]*models.Pod, error)
    DeletePod(ctx, podID string) error
    UpdateQueryStats(ctx, podID string, hit bool, latencyMs int64) error
    UpdateFreshness(ctx, podID string, eventCount int64) error
    GetStats(ctx) (*models.PodStats, error)
}
```

## Pod IDs are the isolation mechanism, not just an identifier

Per [ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md),
a pod ID optionally embeds a `cluster_id` segment:

```go
models.PodID("k8fy.live-state", "", "payments")           // "k8fy.live-state.payments" — today's shape, unscoped
models.PodID("k8fy.live-state", "cluster-42", "payments") // "k8fy.live-state.cluster-42.payments" — fleet-scoped
models.PodID("k8fy.certificates", "cluster-42")           // "k8fy.certificates.cluster-42" — single-global-pod families too
```

This is *why* two clusters reporting the same namespace name don't collide
in the same shard: the registry key (and the underlying Postgres `pod_id`
column — see [STORAGE_BACKENDS.md](STORAGE_BACKENDS.md)) simply differs.
`Pod.TenantID`/`Pod.ClusterID` fields exist too, but are metadata for
introspection — they are **not** part of the DynamoDB key and are not what
provides isolation.

## Usage

```go
// Get the registry from the orchestrator/ingester's constructor args.
reg := orch.GetPodRegistry() // *registry.Cache wrapping *registry.Registry

// Create a leaf pod (this is what Ingester.routeAndCreatePod does on
// first sight of a new shard — you won't normally call this directly).
pod := &models.Pod{
    ID:        models.PodID("k8fy.live-state", clusterID, "payments"),
    Kind:      "leaf",
    Namespace: "k8fy",
    StoreType: "kv",
    Authority: "derived",
    Lifecycle: "active",
    TenantID:  tenantID,
    ClusterID: clusterID,
}
reg.UpsertPod(ctx, pod)

// Query pods in the "k8fy" integration.
integration := "k8fy"
pods, _ := reg.ListPods(ctx, &models.PodFilter{Namespace: &integration})

// Track a query outcome.
reg.UpdateQueryStats(ctx, pod.ID, true, 45) // hit, 45ms latency
```

## DynamoDB table (production)

**Table name:** `agentify-pod-registry` (Terraform: `infra/terraform/aws/`)
**Primary key:** `id` (partition key) — a single string; cluster-scoping
lives inside that string (see above), not in a second key attribute.
**Billing:** on-demand.

**Local/dev:** `REGISTRY_BACKEND=memory` (or simply passing `nil` to
`registry.NewRegistry`) runs the registry as an in-memory map — no AWS
dependency, no LocalStack. This is the default for every test in this repo.

## The cache (ADR 0012)

`registry.Cache` wraps a `PodStore` (DynamoDB or in-memory) with a
read-through snapshot: caches the whole pod set (small, one tenant's fleet
worth), refreshes on TTL, serves a stale snapshot on a refresh error (up to
`maxStale`) instead of failing the request, and invalidates only on
set-changing writes (`UpsertPod`/`DeletePod` — not `UpdateFreshness`/
`UpdateQueryStats`, which are frequent per-ingest updates that shouldn't
thrash the cache). Metrics: `agentify_registry_cache_total{result}`
(`hit`/`miss`/`stale`/`error`).
