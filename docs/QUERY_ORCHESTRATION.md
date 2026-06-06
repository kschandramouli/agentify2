# Query Orchestration

## Overview

Query orchestration is how user questions flow through the system:

```
User query ("Is service X healthy?")
  ├─ Parse intent ("health_check")
  ├─ Route to pods (find k8fy.live-state pods)
  ├─ Fetch from storage backends (Postgres, Redis, etc.)
  ├─ Correlate results (if multi-pod)
  └─ Return data + sources

Data + sources
  ├─ → Agent service (Claude reasoning)
  └─ → Human-readable answer
```

## Components

### Query Executor
- `src/backend/internal/orchestrator/query_executor.go` — main orchestrator
- Methods:
  - `Execute()` — end-to-end query processing
  - `routeToPods()` — determine which pods answer the query
  - `fetchFromPod()` — query storage backend
  - `correlateResults()` — combine multi-pod results

### Intent Parsing
- Implemented inline in `routeToPods()` with switch statement
- TODO: move to `intent_parser.go` for more sophisticated NLP

### Routing Logic
```
Intent              → Pod Selection
──────────────────────────────────
health_check        → k8fy.live-state (KV/passthrough)
cert_check          → k8fy.certificates (relational)
event_search        → k8fy.events (logs/search)
metric_trending     → k8fy.metrics (time-series)
```

## Usage

```go
executor := orchestrator.NewQueryExecutor(registry, backendFactory, logger)

result, err := executor.Execute(ctx, "health_check", query, "prod")
// result = {
//   "k8fy.live-state": [ {...}, {...} ],  // raw data
//   "merged": true
// }
```

## Integration with API

Updated `api/handlers.go`:
- `HandleQuery()` now uses query executor
- Requests are routed to correct pods
- Data is fetched and correlated
- TODO: pass to agent for reasoning

## Files

- `src/backend/internal/orchestrator/query_executor.go` — query execution
- Updated `src/backend/internal/api/handlers.go` — API integration
