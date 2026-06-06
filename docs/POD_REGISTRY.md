# Pod Registry

## What is it?

The pod registry is the **single source of truth** for what pods exist in the context-mesh at runtime. It stores:
- Pod metadata (ID, type, storage backend, lifecycle)
- Pod configuration (partition key for sharding, index mappings)
- Pod statistics (event count, query patterns, freshness)
- Pod hierarchy (parent-child relationships for index pods)

See:
- `context-mesh/policies/pod-formation.md` — lifecycle rules
- `ARCHITECTURE.md` — runtime structure
- `ADR 0002` — recursive pod design

## Files

### Models
- `src/backend/internal/models/pod.go` — Pod, QueryStats, PodFilter data structures

### Registry Client
- `src/backend/internal/storage/registry/registry.go` — DynamoDB client with methods:
  - `UpsertPod()` — create/update pod
  - `GetPod()` — fetch single pod
  - `ListPods()` — query pods with filter
  - `UpdateQueryStats()` — track hits/misses
  - `UpdateFreshness()` — mark pod as updated
  - `GetStats()` — aggregate stats

### Infrastructure
- `infra/aws/dynamodb_pod_registry.tf` — Terraform IaC for DynamoDB table

## Usage

```go
// Get the registry from the orchestrator
reg := orch.GetPodRegistry()

// Create a new pod
pod := &models.Pod{
    ID:        "k8fy.live-state",
    Kind:      "index",
    Summary:   "Live Kubernetes service/pod health, sharded by namespace",
    Namespace: "k8fy",
    StoreType: "passthrough",
    Authority: "derived",
    Lifecycle: "active",
}
reg.UpsertPod(ctx, pod)

// Query pods by namespace
pods, _ := reg.ListPodsByNamespace(ctx, "k8fy")

// Track a query
reg.UpdateQueryStats(ctx, "k8fy.live-state", true, 45) // hit, 45ms latency
```

## DynamoDB Table

**Table name:** `agentify-pod-registry`

**Primary key:** `id` (partition key)

**Indexes:**
- `namespace-lifecycle-index` — query pods by namespace + lifecycle
- `store-type-index` — query pods by storage type

**Attributes:**
- `id`, `kind`, `summary`, `tags`, `namespace`
- `store_type`, `authority`, `schema_ref`
- `partition_key`, `shards` (for index pods)
- `lifecycle`, `freshness`, `event_count`
- `query_stats` (hits, misses, p95_latency_ms)
- `created_at`, `updated_at`

**Billing:** On-demand (scales automatically)

## Deployment

### Local (development)
DynamoDB is an AWS service — not available locally. For dev, use:
- LocalStack (Docker-based DynamoDB emulator)
- Or run against real AWS (free tier)

### AWS (production)
```bash
# Deploy via Terraform
cd infra/aws
terraform apply
```

## Next steps

Once the registry is populated with pods:
1. Event ingestion (spec 001) routes events to pods
2. Orchestrator queries registry to find pods for a query
3. Refinement loop updates pod stats (split/merge decisions)
