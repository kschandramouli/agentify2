# Hub Data Path — Ingestion, Registry, Storage, Query

> Merged 2026-08-04 from four separate files (`POD_REGISTRY.md`,
> `EVENT_INGESTION.md`, `STORAGE_BACKENDS.md`, `QUERY_ORCHESTRATION.md`) —
> each was small, told one quarter of the same story, and repeated the same
> "this is Hub-only" framing. One doc now covers the whole write→register→
> store→read path.

**Everything in this file is Hub-only.** The pod registry, Postgres, and
the query executor all live inside the single central Hub process
(`src/backend/`). Discovery (`agentify-discovery`, the per-cluster fleet
collector) never touches any of it directly — it only ever POSTs to Hub
HTTP endpoints (`/api/ingest`, `/api/cluster-inventory`,
`/api/cluster-ingress`, `/api/cluster-health`, `/api/service-dependencies`),
and it's the Hub's own handlers that turn those pushes into the rows and
pod-registry entries described below. For what Discovery itself does, see
[AGENT_INTEGRATION.md](AGENT_INTEGRATION.md)'s "Fleet clusters & live
drill-down" section instead.

---

## Event Ingestion

> Predates the Postgres consolidation ([ADR 0010](../context-mesh/decisions/0010-postgres-single-store.md))
> and the multi-tenancy/cluster-scoping work
> ([ADR 0022](../context-mesh/decisions/0022-multi-tenant-fleet-hub.md)/
> [0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md)).
> See [spec 001](../context-mesh/specs/001-event-ingestion.md) for the
> original design spec this section implements.

Event ingestion is the gateway for all data flowing into agentify's
Postgres-backed store. It:
1. Accepts canonical events at `POST /api/ingest`
2. Resolves the pushing adapter's tenant/cluster identity from its bearer
   credential (or defaults to the single-deployment sentinel if none is
   presented)
3. Classifies events by traits (storage-strategy policy) and routes to a pod
4. Creates the pod (and its parent index pod, for sharded families) if it
   doesn't exist yet
5. Stores the event in Postgres, tagged with the resolved tenant/cluster
6. Updates the pod registry (freshness, event count)

### Files

- `src/backend/internal/models/event.go` — `Event`, `EventTraits`, `EventIngestionResult`
- `src/backend/internal/models/shard.go` — `PodID`, the cluster-aware pod-ID builder ingestion uses
- `src/backend/internal/ingestion/ingester.go`:
  - `Ingest(ctx, event, tenantID, clusterID)` — entry point. `tenantID`/`clusterID` are **never** read from the event body — they come from the caller having already resolved them from a credential, same trust boundary as every other tenant-scoped write in this codebase.
  - `routeAndCreatePod(ctx, event, tenantID, clusterID)` — determines the target pod ID via `models.PodID(profile.EventNamespace, clusterID, partition)`; creates the leaf (and parent index) pod if new.
  - `storeEvent(ctx, pod, event, tenantID, clusterID)` — writes `tenant_id`/`cluster_id` into the backend's `data` map alongside `event_namespace`/`type`/`source` (no `storage.Backend` interface change — see "Storage Backends" below).
  - `deriveStoreType()` — storage-strategy decision matrix for namespaces with no registered profile.
- `src/backend/internal/api/handlers.go` — `HandleIngestEvent` (`POST /api/ingest`)

### Who calls this today

- **`src/adapters/k8fy/`** (the original K8fy adapter, Python — see
  [K8FY_ADAPTER.md](K8FY_ADAPTER.md)) — **does** call `/api/ingest`. Its
  `Emitter` already sends a Bearer token (`BACKEND_AUTH_TOKEN`) with every
  push; as of ADR 0024 the Hub actually checks that token and, if it
  matches an `Integration`'s `collector_token`, resolves real
  `(tenant_id, cluster_id)`. No adapter code change was needed for this —
  only the Hub-side check was missing.
- **`src/adapters/discovery/`** (`agentify-discovery`, i.e. Discovery, the
  newer fleet collector) **never** calls `/api/ingest` — it pushes to the
  Hub's `/api/cluster-inventory`, `/api/cluster-ingress`, and
  `/api/cluster-health` instead (see ROADMAP P18), none of which go
  through the ingestion/pod-mesh path this section describes.

### Credential resolution (ADR 0022/0024)

`HandleIngestEvent` calls `resolveTenantContext(r)` — the same helper every
collector-facing endpoint uses:

| Presented credential | Result |
|---|---|
| None | `(DefaultTenantID, "")` — today's single-cluster behavior, byte-for-byte unchanged. This is what keeps every adapter deployment that hasn't been given a `CollectorToken` working exactly as before. |
| Doesn't match any `Integration.CollectorToken` | Rejected, 401 |
| Matches an `Integration.CollectorToken` | That `Integration`'s `(tenant_id, cluster_id)` |

### Usage

```go
event := &models.Event{
    ID:             uuid.New().String(),
    Timestamp:      time.Now(),
    EventNamespace: "k8fy.live-state",
    Type:           "pod_modified",
    Source:         "kubernetes-api",
    EntityKey:      "payment-svc-abc",  // current-state key (latest-wins)
    Payload: map[string]interface{}{
        "pod_id":    "payment-svc-abc",
        "namespace": "prod",
        "reason":    "CrashLoopBackOff",
    },
    Traits: models.EventTraits{
        Shape: "structured", AccessPattern: "point-lookup",
        Temporality: "current-state", Mutability: "mutable", Authority: "derived",
    },
}

resp, _ := http.Post(
    "http://localhost:8080/api/ingest",
    "application/json",
    marshal(event),
)
// Optionally: req.Header.Set("Authorization", "Bearer "+collectorToken)
```

```
Event arrives at POST /api/ingest
    ├─ resolveTenantContext(r) → (tenant_id, cluster_id)
    ├─ routeAndCreatePod() → cluster-aware pod ID (models.PodID)
    ├─ storeEvent() → Postgres (current_state or events table)
    ├─ registry.UpdateFreshness()
    └─ (refinement-loop feedback emission is still a TODO — see ingester.go)
```

### Storage-Strategy Decision Matrix

Event traits → storage backend (from `storage-strategy.md`); "timeseries"
and "logs" both alias to the same Postgres `events` table today (ADR 0013)
— there is no separate TSDB or log-search index yet:

| Access Pattern | Store Type | Realized as |
|---|---|---|
| point-lookup, current-state | **kv** | `current_state` table |
| filter + aggregate, structured | **relational** | `events` table |
| time-range-scan, numeric/metric | **timeseries** | `events` table (aliased, ADR 0013) |
| time-range-scan, append-only | **logs** | `events` table (aliased, ADR 0013) |
| similarity / semantic | **vector** | unprovisioned at pod-mesh level — see "Storage Backends" below |
| relationship-traversal | **graph** | unprovisioned |

### API Endpoint

```
POST /api/ingest
Content-Type: application/json
Authorization: Bearer <collector_token>   (optional — see credential table above)

{
  "id": "evt-123",
  "timestamp": "2026-08-03T10:00:00Z",
  "event_namespace": "k8fy.live-state",
  "type": "pod_modified",
  "source": "kubernetes-api",
  "entity_key": "payment-svc-abc",
  "payload": { "pod_id": "payment-svc-abc", "namespace": "prod", "ready": true },
  "traits": { "shape": "structured", "access_pattern": "point-lookup", "temporality": "current-state", "mutability": "mutable", "authority": "derived" }
}

Response (202 Accepted):
{
  "event_id": "evt-123",
  "pod_id": "k8fy.live-state.prod",
  "created_pod": true,
  "store_type": "kv",
  "latency_ms": 12,
  "timestamp": "2026-08-03T10:00:00Z"
}
```

Related admin endpoints: `GET /admin/pods` (list all active pods),
`GET /admin/pods/get?id=...` (get single pod).

---

## Pod Registry

The pod registry is the **runtime source of truth** for what pods exist in
the context-mesh — logical storage shards, not Kubernetes pods (see
[glossary.md](../context-mesh/glossary.md) if that distinction is new). It
stores:
- Pod metadata (ID, kind, storage backend, lifecycle)
- Pod configuration (partition key for sharding, index shard maps)
- Pod statistics (event count, query hit/miss/latency)
- Pod hierarchy (index pods hold a shard map over their child leaf pods, ADR 0002)
- Tenant/cluster metadata (`TenantID`/`ClusterID` — see "Pod IDs are the isolation mechanism" below)

See also: `context-mesh/policies/pod-formation.md` (lifecycle rules),
[ADR 0002](../context-mesh/decisions/0002-pods-are-recursive.md) (recursive
pod design), [ADR 0012](../context-mesh/decisions/0012-pod-registry-cache.md)
(the read-through cache described below), [ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md)
(cluster-aware pod IDs).

### Files

- `src/backend/internal/models/pod.go` — `Pod`, `ShardRef`, `QueryStats`, `PodFilter`, `PodStats`
- `src/backend/internal/models/shard.go` — `PodID(parts ...string) string`, the cluster-aware pod-ID builder (ADR 0024)
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

### Pod IDs are the isolation mechanism, not just an identifier

Per [ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md),
a pod ID optionally embeds a `cluster_id` segment:

```go
models.PodID("k8fy.live-state", "", "payments")           // "k8fy.live-state.payments" — today's shape, unscoped
models.PodID("k8fy.live-state", "cluster-42", "payments") // "k8fy.live-state.cluster-42.payments" — fleet-scoped
models.PodID("k8fy.certificates", "cluster-42")           // "k8fy.certificates.cluster-42" — single-global-pod families too
```

This is *why* two clusters reporting the same namespace name don't collide
in the same shard: the registry key (and the underlying Postgres `pod_id`
column — see "Storage Backends" below) simply differs. `Pod.TenantID`/
`Pod.ClusterID` fields exist too, but are metadata for introspection — they
are **not** part of the DynamoDB key and are not what provides isolation.

### Usage

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

### DynamoDB table (production)

**Table name:** `agentify-pod-registry` (Terraform: `infra/terraform/aws/`)
**Primary key:** `id` (partition key) — a single string; cluster-scoping
lives inside that string (see above), not in a second key attribute.
**Billing:** on-demand.

**Local/dev:** `REGISTRY_BACKEND=memory` (or simply passing `nil` to
`registry.NewRegistry`) runs the registry as an in-memory map — no AWS
dependency, no LocalStack. This is the default for every test in this repo.

### The cache (ADR 0012)

`registry.Cache` wraps a `PodStore` (DynamoDB or in-memory) with a
read-through snapshot: caches the whole pod set (small, one tenant's fleet
worth), refreshes on TTL, serves a stale snapshot on a refresh error (up to
`maxStale`) instead of failing the request, and invalidates only on
set-changing writes (`UpsertPod`/`DeletePod` — not `UpdateFreshness`/
`UpdateQueryStats`, which are frequent per-ingest updates that shouldn't
thrash the cache). Metrics: `agentify_registry_cache_total{result}`
(`hit`/`miss`/`stale`/`error`).

---

## Storage Backends

> The original Postgres/Redis/Weaviate polyglot design was collapsed to a
> single Postgres instance in [ADR 0010](../context-mesh/decisions/0010-postgres-single-store.md)
> (2026-06-02); Redis and Weaviate were removed from the runtime, config, and
> dependency tree entirely.

Every pod-mesh store type is backed by **one Postgres instance** (RDS in
production). The trait→store-family classification in
[storage-strategy.md](../context-mesh/policies/storage-strategy.md) still
exists conceptually (it decides which table/query shape an event gets), but
the number of *engines* is one, not up to six.

Each backend implements `storage.Backend` (`src/backend/internal/storage/backend.go`):
- `Store(ctx, podID, data)` — write data
- `Query(ctx, podID, criteria)` — read data
- `HealthCheck(ctx)` — verify connectivity
- `Close()` — cleanup

`BackendFactory.GetBackend(storeType)` maps a pod's `StoreType` to a backend:
- `"relational"`, `"timeseries"`, `"logs"` → the same Postgres `Client` (events table) — the latter two are trait-derived store types that alias to relational per [ADR 0013](../context-mesh/decisions/0013-temporal-data-in-postgres-events-table.md); no separate TSDB/log index exists yet.
- `"kv"` → `Client.CurrentStateStore()` (current_state table)
- `"vector"` → **`nil`**, deliberately unprovisioned at the pod-mesh routing level (`orchestrator/router.go`'s `buildBackendFactory`) — no feature needs pod-mesh-routed semantic search yet.

### Postgres — the only engine

One `*sql.DB` connection pool (`src/backend/internal/storage/postgres/postgres.go`)
backs everything below. Schema is managed inline via idempotent
`CREATE TABLE IF NOT EXISTS`/`ALTER TABLE IF EXISTS ... ADD COLUMN IF NOT EXISTS`
in `initSchema()` — there's no separate migration tool/file set.

#### `events` table (relational / append-only)

One row per event, keyed by its own UUID. Backs `k8fy.events` (deploy/change
history) and `k8fy.metrics` (restart time-series, [ADR 0013](../context-mesh/decisions/0013-temporal-data-in-postgres-events-table.md)).

```sql
CREATE TABLE events (
    id UUID PRIMARY KEY,
    pod_id TEXT NOT NULL,
    event_namespace TEXT NOT NULL,
    event_type TEXT NOT NULL,
    timestamp TIMESTAMP NOT NULL,
    payload JSONB NOT NULL,
    tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',  -- ADR 0022/0024
    cluster_id TEXT,                                       -- ADR 0022/0024
    created_at TIMESTAMP DEFAULT NOW()
);
```

#### `current_state` table (kv / latest-wins)

One row per `(pod_id, entity_key)`, upserted on every write. Backs
`k8fy.live-state` (pod/service health) and `k8fy.certificates`.

```sql
CREATE TABLE current_state (
    pod_id TEXT NOT NULL,
    entity_key TEXT NOT NULL,
    event_namespace TEXT NOT NULL,
    event_type TEXT NOT NULL,
    source TEXT NOT NULL,
    payload JSONB NOT NULL,
    tenant_id TEXT NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',  -- ADR 0022/0024
    cluster_id TEXT,                                       -- ADR 0022/0024
    updated_at TIMESTAMP,
    PRIMARY KEY (pod_id, entity_key)
);
```

**Isolation for both tables is via `pod_id`, not `tenant_id`/`cluster_id`
filtering** ([ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md)):
`models.PodID` (`internal/models/shard.go`) folds a resolved `cluster_id`
into the pod ID itself (e.g. `"k8fy.live-state.cluster-42.payments"` instead
of `"k8fy.live-state.payments"`) so two clusters' identically-named
namespaces land in different rows. The `tenant_id`/`cluster_id` columns are
written for observability, not filtered on in `Query` — RLS was deliberately
rejected for these two tables (too many untouched call sites — retention
janitor, `TrackedEntities`, every Tier-1 tool — would silently return zero
rows if a caller forgot `setTenantContext`; see the ADR's "Isolation
mechanism" section).

#### Tenant-scoped tables with real RLS

Unlike `events`/`current_state`, a handful of newer tables *do* use
Postgres Row-Level Security, enforced via `setTenantContext` (a
`SELECT set_config('app.current_tenant_id', $1, true)` inside the same
transaction) + a `tenant_isolation` policy + `FORCE ROW LEVEL SECURITY`:

- **`service_dependencies`** ([ADR 0022](../context-mesh/decisions/0022-multi-tenant-fleet-hub.md)) — the mined service-call graph (`k8fy/service_topology.py`).
- **`cluster_services`** ([ADR 0023](../context-mesh/decisions/0023-service-cluster-resolver.md)) — the service→cluster registry `GET /api/resolve-cluster` reads.
- **`cluster_ingress_endpoints`** (ROADMAP P18 use case #3) — the
  Ingress/Gateway+HTTPRoute/OpenShift Route entry-point map Discovery's
  ingress scan populates via `POST /api/cluster-ingress`.
- **`cluster_health_snapshots`** (ROADMAP P18 use case #5) — one row per
  cluster, overwritten in place (`ON CONFLICT DO UPDATE`, not a delete-then-
  insert set like the three tables above) with that cluster's current pod-
  readiness/K8s-version snapshot, populated via `POST /api/cluster-health`.

These have few enough call sites (all touched together when RLS was added)
that a missed `setTenantContext` call is an acceptable, auditable risk —
the opposite tradeoff from `events`/`current_state` above.

#### Other Postgres-backed tables (not pod-mesh-routed)

`integrations`, `chat_sessions`, `remediation_proposals`, `traces`,
`model_pricing`, `incident_embeddings` — accessed directly via their own
`Client` methods (`CreateIntegration`, `CreateRemediationProposal`, etc.),
not through the generic `Store`/`Query`/`BackendFactory` path. Most of these
also carry `tenant_id`/`cluster_id` columns (ADR 0022 phase 1) with the same
`DefaultTenantID`-on-migration guarantee, but (except `service_dependencies`
above) no RLS yet. `integrations` additionally carries `token_secret_arn`
([ADR 0025](../context-mesh/decisions/0025-integration-token-secrets-manager.md)) —
when `INTEGRATION_SECRETS_PREFIX` is configured, the outbound adapter
credential lives in AWS Secrets Manager and only an ARN reference is stored
here, mutually exclusive with the legacy plaintext `token` column.

#### pgvector (semantic memory, P8)

`incident_embeddings` uses the `vector` Postgres extension for cosine-
similarity search over past diagnoses (`GET /api/incidents/similar`,
consumed by the agent's `get_similar_incidents` tool). This is a **direct**
Postgres-client feature, separate from the pod-mesh's unprovisioned
`"vector"` store type mentioned above — the two are easy to conflate but
serve different call paths. See [ADR 0018](../context-mesh/decisions/0018-three-layer-memory-architecture.md).

### Files

- `src/backend/internal/storage/backend.go` — `Backend` interface + `BackendFactory`
- `src/backend/internal/storage/postgres/postgres.go` — the only backend implementation (`Client` for relational, `CurrentState` for kv, plus the direct-access tables above)
- `src/backend/internal/models/shard.go` — `models.PodID`, the cluster-aware pod-ID helper (ADR 0024)
- `src/backend/internal/orchestrator/router.go` — `buildBackendFactory` (where `vector` is wired to `nil`)
- `infra/terraform/aws/` — RDS Postgres provisioning

---

## Query Orchestration

> Uses lowercase, unexported method names historically
> (`routeToPods()`/`fetchFromPod()`) predate the two-tier query path
> ([ADR 0006](../context-mesh/decisions/0006-two-tier-query-path.md)) and
> cluster-aware routing
> ([ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md)) —
> the real, current API is exported (`RouteToPods`/`FetchFromPod`), as
> shown below.

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

### Usage

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

### Files

- `src/backend/internal/orchestrator/query_executor.go` — routing + fetch + correlate
- `src/backend/internal/orchestrator/query_executor_test.go` — cluster-scoping regression tests
- `src/backend/internal/api/handlers.go` — `HandleQuery`, `HandleAgentFetch`, `mapToolToQuery`
- `src/backend/internal/models/shard.go` — `models.PodID`, the cluster-aware pod-ID builder both routing paths that support `clusterID` use
