# Event Ingestion

> Rewritten 2026-08-03 — the previous version predated the Postgres
> consolidation ([ADR 0010](../context-mesh/decisions/0010-postgres-single-store.md))
> and the multi-tenancy/cluster-scoping work
> ([ADR 0022](../context-mesh/decisions/0022-multi-tenant-fleet-hub.md)/
> [0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md)).
> `Ingest()`'s signature and `routeAndCreatePod`'s pod-ID logic have both
> changed since. See [spec 001](../context-mesh/specs/001-event-ingestion.md)
> for the original design spec this doc implements.

## What is it?

Event ingestion is the gateway for all data flowing into agentify's
Postgres-backed store. It:
1. Accepts canonical events at `POST /api/ingest`
2. Resolves the pushing collector/adapter's tenant/cluster identity from its
   bearer credential (or defaults to the single-deployment sentinel if none
   is presented)
3. Classifies events by traits (storage-strategy policy) and routes to a pod
4. Creates the pod (and its parent index pod, for sharded families) if it
   doesn't exist yet
5. Stores the event in Postgres, tagged with the resolved tenant/cluster
6. Updates the pod registry (freshness, event count)

## Files

### Models
- `src/backend/internal/models/event.go` — `Event`, `EventTraits`, `EventIngestionResult`
- `src/backend/internal/models/shard.go` — `PodID`, the cluster-aware pod-ID builder ingestion uses

### Ingestion service
- `src/backend/internal/ingestion/ingester.go`:
  - `Ingest(ctx, event, tenantID, clusterID)` — entry point. `tenantID`/`clusterID` are **never** read from the event body — they come from the caller having already resolved them from a credential (see below), same trust boundary as every other tenant-scoped write in this codebase.
  - `routeAndCreatePod(ctx, event, tenantID, clusterID)` — determines the target pod ID via `models.PodID(profile.EventNamespace, clusterID, partition)`; creates the leaf (and parent index) pod if new.
  - `storeEvent(ctx, pod, event, tenantID, clusterID)` — writes `tenant_id`/`cluster_id` into the backend's `data` map alongside `event_namespace`/`type`/`source` (no `storage.Backend` interface change — see [STORAGE_BACKENDS.md](STORAGE_BACKENDS.md)).
  - `deriveStoreType()` — storage-strategy decision matrix for namespaces with no registered profile.

### HTTP handler
- `src/backend/internal/api/handlers.go` — `HandleIngestEvent` (`POST /api/ingest`)

## Who calls this today

- **`src/adapters/k8fy/`** (the original K8fy adapter, Python — see
  [K8FY_ADAPTER.md](K8FY_ADAPTER.md)) — its `Emitter` already sends a Bearer
  token (`BACKEND_AUTH_TOKEN`) with every push; as of ADR 0024 that token is
  actually checked and, if it matches an `Integration`'s `collector_token`,
  resolves real `(tenant_id, cluster_id)`. No adapter code change was needed
  for this — only the Hub-side check was missing.
- **`src/adapters/discovery/`** (`agentify-discovery`, the newer fleet
  collector) doesn't call `/api/ingest` at all — it pushes to
  `/api/cluster-inventory` and `/api/service-dependencies` instead (see
  ROADMAP P18).

## Credential resolution (ADR 0022/0024)

`HandleIngestEvent` calls `resolveTenantContext(r)` — the same helper every
collector-facing endpoint uses:

| Presented credential | Result |
|---|---|
| None | `(DefaultTenantID, "")` — today's single-cluster behavior, byte-for-byte unchanged. This is what keeps every adapter deployment that hasn't been given a `CollectorToken` working exactly as before. |
| Doesn't match any `Integration.CollectorToken` | Rejected, 401 |
| Matches an `Integration.CollectorToken` | That `Integration`'s `(tenant_id, cluster_id)` |

## Usage

### Emitting a canonical event

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

### Workflow

```
Event arrives at POST /api/ingest
    ├─ resolveTenantContext(r) → (tenant_id, cluster_id)
    ├─ routeAndCreatePod() → cluster-aware pod ID (models.PodID)
    ├─ storeEvent() → Postgres (current_state or events table)
    ├─ registry.UpdateFreshness()
    └─ (refinement-loop feedback emission is still a TODO — see ingester.go)
```

## Storage-Strategy Decision Matrix

Event traits → storage backend (from `storage-strategy.md`); "timeseries"
and "logs" both alias to the same Postgres `events` table today (ADR 0013)
— there is no separate TSDB or log-search index yet:

| Access Pattern | Store Type | Realized as |
|---|---|---|
| point-lookup, current-state | **kv** | `current_state` table |
| filter + aggregate, structured | **relational** | `events` table |
| time-range-scan, numeric/metric | **timeseries** | `events` table (aliased, ADR 0013) |
| time-range-scan, append-only | **logs** | `events` table (aliased, ADR 0013) |
| similarity / semantic | **vector** | unprovisioned at pod-mesh level — see [STORAGE_BACKENDS.md](STORAGE_BACKENDS.md)'s pgvector note |
| relationship-traversal | **graph** | unprovisioned |

## API Endpoint

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

### Related admin endpoints

```
GET /admin/pods              # List all active pods
GET /admin/pods/get?id=...   # Get single pod
```
