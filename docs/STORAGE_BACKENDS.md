# Storage Backends

## Overview

The storage layer provides a pluggable abstraction for different data stores:
- **Postgres** — relational/append-only (events, logs)
- **Redis** — key-value (live-state, current values)
- **Weaviate** — vector (semantic search)

Each backend implements the `Backend` interface:
- `Store(podID, data)` — write data
- `Query(podID, criteria)` — read data
- `HealthCheck()` — verify connectivity
- `Close()` — cleanup

## Postgres

Stores events/logs (append-only, searchable by time range).

### Schema
- `events` table: id, pod_id, event_namespace, event_type, timestamp, payload, created_at
- Indexes: pod_id, timestamp, namespace

### Usage
```go
pg := postgres.NewClient(connStr, logger)
pg.Store(ctx, "k8fy.events", eventData)
pg.Query(ctx, "k8fy.events", map{"pod_id": "..."))
```

## Redis

Stores current-state data (KV with TTL).

### Key format
`pod_id:key` (e.g., `k8fy.live-state:payment-svc`)

### Usage
```go
rdb := redis.NewClient("localhost:6379", logger)
rdb.Store(ctx, "k8fy.live-state", {"id": "payment-svc", "phase": "Running"})
rdb.Query(ctx, "k8fy.live-state", {"key": "payment-svc"})
```

## Weaviate

Already implemented; stores vectors for semantic search.

## Routing

The `BackendFactory` routes events to backends based on `storeType`:
- `relational` → Postgres
- `kv` → Redis
- `vector` → Weaviate

## Integration with Ingestion

The ingestion layer now stores events:

```
Event arrives
  ├─ Classify (storage-strategy)
  ├─ Route to pod
  ├─ Create pod if needed
  ├─ Get backend for pod.StoreType
  ├─ Store event in backend ← NEW
  ├─ Update pod registry
  └─ Emit observation
```

## Files

- `src/backend/internal/storage/backend.go` — interface + factory
- `src/backend/internal/storage/postgres/postgres.go` — Postgres impl
- `src/backend/internal/storage/redis/redis.go` — Redis impl
- `infra/aws/rds.tf` — Terraform for Postgres (RDS)
