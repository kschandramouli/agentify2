# Event Ingestion (Spec 001)

## What is it?

Event ingestion is the **gateway** for all data flowing into agentify. It:
1. Accepts canonical events from adapters (K8s watcher, CRM sync, webhooks, etc.)
2. Classifies events by traits (via storage-strategy policy)
3. Routes events to the appropriate pod(s)
4. Creates pods if needed (via pod-formation rules)
5. Stores events in the target storage backend
6. Updates the pod registry
7. Emits feedback for the refinement loop

See `context-mesh/specs/001-event-ingestion.md` for the full spec.

## Files

### Models
- `src/backend/internal/models/event.go` — Event, EventTraits, EventIngestionResult

### Ingestion Service
- `src/backend/internal/ingestion/ingester.go` — main logic:
  - `Ingest()` — entry point
  - `routeAndCreatePod()` — determine target pod
  - `deriveStoreType()` — storage-strategy decision matrix

### HTTP Handler
- Updated `src/backend/internal/api/handlers.go` — `/api/ingest` endpoint
- Updated `src/backend/internal/api/router.go` — add ingestion routes

## Usage

### From an adapter (K8s event watcher)

```go
// Emit a canonical event
event := &models.Event{
    ID:             uuid.New().String(),
    Timestamp:      time.Now(),
    EventNamespace: "k8fy.events",
    Type:           "pod_restart",
    Source:         "kubernetes-api",
    Payload: map[string]interface{}{
        "pod_id":    "payment-svc-abc",
        "namespace": "prod",
        "reason":    "CrashLoopBackOff",
        "message":   "Connection refused...",
    },
    Text: ptr("Connection refused to database..."),
    Traits: models.EventTraits{
        Shape:         "semi-structured",
        AccessPattern: "time-range-scan",
        Temporality:   "append-only",
        Mutability:    "immutable",
        Authority:     "derived",
        Retention:     "30d",
    },
}

// HTTP POST
resp, _ := http.Post(
    "http://localhost:8080/api/ingest",
    "application/json",
    marshal(event),
)

// Response: which pod it was stored in, latency, pod creation status
```

### Workflow

```
Event arrives
    │
    ▼
Ingest service
    ├─ Classify by traits (from storage-strategy)
    ├─ Route to pod (or create new one)
    ├─ Store in target backend (Postgres, Redis, Weaviate, TSDB, etc.)
    ├─ Update pod registry (freshness, event count)
    └─ Emit observation (SQS/Kafka → refinement loop)

Refinement loop observes
    ├─ Pod miss rates, latency
    ├─ Decides: split/merge/migrate?
    └─ Updates pod registry
```

## Storage-Strategy Decision Matrix

Event traits → Storage type mapping (from storage-strategy.md):

| Access Pattern | Store Type |
|---|---|
| similarity / semantic | **vector** (Weaviate) |
| point-lookup, current-state | **kv** (Redis) |
| filter + aggregate, structured | **relational** (Postgres) |
| relationship-traversal | **graph** |
| time-range-scan, time-series | **timeseries** (TSDB) |
| time-range-scan, append-only | **logs** (Elasticsearch/Loki) |

See `context-mesh/policies/storage-strategy.md` for full classification rules.

## API Endpoints

### Ingest an event

```
POST /api/ingest
Content-Type: application/json

{
  "id": "evt-123",
  "timestamp": "2026-05-31T10:00:00Z",
  "event_namespace": "k8fy.events",
  "type": "pod_restart",
  "source": "kubernetes-api",
  "payload": { ... },
  "text": "Connection refused...",
  "traits": {
    "shape": "semi-structured",
    "access_pattern": "time-range-scan",
    "temporality": "append-only",
    "mutability": "immutable",
    "authority": "derived",
    "retention": "30d"
  }
}

Response (202 Accepted):
{
  "event_id": "evt-123",
  "pod_id": "k8fy.events.pod_restart",
  "created_pod": true,
  "store_type": "logs",
  "latency_ms": 45,
  "timestamp": "2026-05-31T10:00:00Z"
}
```

### View pod registry

```
GET /admin/pods              # List all active pods
GET /admin/pods/get?id=...  # Get single pod
```

## Next steps

Now that ingestion is scaffolded:
1. Implement **storage backends** (Postgres, Redis, Weaviate, TSDB)
2. Implement **adapter workers** (K8s event watcher, cert scraper)
3. Implement **refinement loop** (observe pod stats, make decisions)
4. Implement **query orchestration** (route queries to pods, fetch/correlate)
