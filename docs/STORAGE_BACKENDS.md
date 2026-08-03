# Storage Backends

> Rewritten 2026-08-03 — the previous version described the original
> Postgres/Redis/Weaviate polyglot design. That was collapsed to a single
> Postgres instance in [ADR 0010](../context-mesh/decisions/0010-postgres-single-store.md)
> (2026-06-02); Redis and Weaviate were removed from the runtime, config, and
> dependency tree entirely. This doc now matches what's actually running.

## Overview

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

## Postgres — the only engine

One `*sql.DB` connection pool (`src/backend/internal/storage/postgres/postgres.go`)
backs everything below. Schema is managed inline via idempotent
`CREATE TABLE IF NOT EXISTS`/`ALTER TABLE IF EXISTS ... ADD COLUMN IF NOT EXISTS`
in `initSchema()` — there's no separate migration tool/file set.

### `events` table (relational / append-only)

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

### `current_state` table (kv / latest-wins)

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

### Tenant-scoped tables with real RLS

Unlike `events`/`current_state`, a handful of newer tables *do* use
Postgres Row-Level Security, enforced via `setTenantContext` (a
`SELECT set_config('app.current_tenant_id', $1, true)` inside the same
transaction) + a `tenant_isolation` policy + `FORCE ROW LEVEL SECURITY`:

- **`service_dependencies`** ([ADR 0022](../context-mesh/decisions/0022-multi-tenant-fleet-hub.md)) — the mined service-call graph (`k8fy/service_topology.py`).
- **`cluster_services`** ([ADR 0023](../context-mesh/decisions/0023-service-cluster-resolver.md)) — the service→cluster registry `GET /api/resolve-cluster` reads.

These have few enough call sites (all touched together when RLS was added)
that a missed `setTenantContext` call is an acceptable, auditable risk —
the opposite tradeoff from `events`/`current_state` above.

### Other Postgres-backed tables (not pod-mesh-routed)

`integrations`, `chat_sessions`, `remediation_proposals`, `traces`,
`model_pricing`, `incident_embeddings` — accessed directly via their own
`Client` methods (`CreateIntegration`, `CreateRemediationProposal`, etc.),
not through the generic `Store`/`Query`/`BackendFactory` path. Most of these
also carry `tenant_id`/`cluster_id` columns (ADR 0022 phase 1) with the same
`DefaultTenantID`-on-migration guarantee, but (except `service_dependencies`
above) no RLS yet.

### pgvector (semantic memory, P8)

`incident_embeddings` uses the `vector` Postgres extension for cosine-
similarity search over past diagnoses (`GET /api/incidents/similar`,
consumed by the agent's `get_similar_incidents` tool). This is a **direct**
Postgres-client feature, separate from the pod-mesh's unprovisioned
`"vector"` store type mentioned above — the two are easy to conflate but
serve different call paths. See [ADR 0018](../context-mesh/decisions/0018-three-layer-memory-architecture.md).

## Files

- `src/backend/internal/storage/backend.go` — `Backend` interface + `BackendFactory`
- `src/backend/internal/storage/postgres/postgres.go` — the only backend implementation (`Client` for relational, `CurrentState` for kv, plus the direct-access tables above)
- `src/backend/internal/models/shard.go` — `models.PodID`, the cluster-aware pod-ID helper (ADR 0024)
- `src/backend/internal/orchestrator/router.go` — `buildBackendFactory` (where `vector` is wired to `nil`)
- `infra/terraform/aws/` — RDS Postgres provisioning
