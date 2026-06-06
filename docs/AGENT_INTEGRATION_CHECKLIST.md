# Agent Integration Implementation Checklist

## Completed

✅ **Backend Query Handler** (`src/backend/internal/api/handlers.go`)
- HandleQuery endpoint implemented
- Extracts question and context from request
- Infers intent from natural language question

✅ **Intent Inference** (`inferIntent()` in handlers.go)
- Recognizes health queries ("health", "healthy")
- Recognizes certificate queries ("certificate", "cert", "expir")
- Recognizes metrics queries ("metric", "cpu", "memory")
- Falls back to general_query

✅ **Query Executor** (`src/backend/internal/orchestrator/query_executor.go`)
- Public methods: RouteToPods, FetchFromPod, Execute
- Routes queries to pods based on intent
- Fetches data from pods using backends
- Updates pod statistics

✅ **Agent Client** (`src/backend/internal/api/agent_client.go`)
- Communicates with Python agent service via HTTP
- Sends: intent, raw pod data, context
- Receives: answer, reasoning, confidence, sources
- Includes timeout and error handling

✅ **Request/Response Types** (`src/backend/internal/api/types.go`)
- QueryRequest: question, context
- QueryResponse: answer, status, confidence, sources

✅ **Router Backend Factory** (`src/backend/internal/orchestrator/router.go`)
- GetBackendFactory() method added
- Returns BackendFactory for storage backend selection
- Lazy initialization of backends

✅ **Configuration** (`src/backend/internal/config/env.go`)
- AGENT_SERVICE_URL config already present
- Defaults to http://localhost:8001

✅ **Router Integration** (`src/backend/internal/api/router.go`)
- Passes agentServiceURL to NewHandler

✅ **Main Entry Point** (`src/backend/cmd/agentify/main.go`)
- Passes cfg.AgentServiceURL to router

✅ **Python Agent Service** (`src/agent/`)
- /reason endpoint already implemented
- Calls Claude API with system prompt
- QueryRequest model defined (expects intent, data, context)
- Returns AgentResponse (answer, reasoning, confidence, sources)

✅ **Documentation** (`docs/AGENT_INTEGRATION.md`)
- Full architecture overview
- Data flow diagram
- Configuration guide
- Usage examples
- Testing instructions
- Troubleshooting guide

## Done since this checklist was first written (validated live 2026-06-01)

The full `ingest → store → query → agent → answer` slice now runs end-to-end
(verified with K8s-shaped events → pod formation → Redis → routed query → Opus 4.8
→ correct DEGRADED verdict). Specifically:

✅ **Backend storage wired** — Postgres + Redis connected via `buildBackendFactory`
in `orchestrator/router.go`; `GetBackend` errors on unconfigured backends; graceful
`Close()` on shutdown. (The old `createPostgresBackend()/...nil` stubs are gone.)

✅ **Ingestion stores events** — `ingester.go` persists to the selected backend,
keyed by entity for current-state (latest-wins); pods/shards form per ADR 0005.

✅ **Tool-Calling Loop** — the agent runs a real agentic loop (`agent.py`) with
tools that call back to the backend (`POST /api/agent/fetch`).

✅ **Structured Output** — `output_config.format` + a Pydantic schema; real
`confidence`/`status`/`recommendations` (no more hardcoded 0.85).

✅ **Prompt Caching** — `cache_control` on the system prompt (see caveat re: the
4096-token minimum on Opus 4.8).

✅ **Model** — migrated off the retired `claude-3-5-sonnet-20241022` to
`claude-opus-4-8`.

✅ **Local-dev registry** — in-memory pod registry mode (`REGISTRY_BACKEND=memory`)
so the stack runs without AWS.

## Open / next (now tracked in context-mesh)

The remaining work is now governed by the design docs, not this checklist:

- 🔜 **Two-tier query path** — *the current priority.* Every query routes through
  the LLM today (~13s, costly, non-deterministic); answer deterministic intents
  directly from the store and reserve the LLM for synthesis. See
  [ADR 0006](../context-mesh/decisions/0006-two-tier-query-path.md).
- ⬜ **Enterprise hardening & expansion** — egress/redaction, storage
  consolidation, multi-tenancy, audit/provenance, observability, classical-ML
  signals, correlation. Ranked in [ROADMAP](../context-mesh/ROADMAP.md).
- ⬜ **Adapter emit** — the Python K8fy adapter posts to `/api/ingest`; not yet
  run against a live cluster (validated via direct ingest).

## How It Works End-to-End

1. **User sends query:**
   ```
   POST /api/query
   {"question": "Is payment service healthy?", "context": {"namespace": "prod"}}
   ```

2. **Backend processes:**
   - Infers intent: "health_check"
   - Routes to live-state pods
   - Fetches current pod status from registry

3. **Agent service reasons:**
   - Receives intent, pod data, context
   - Calls Claude API with system prompt
   - Claude analyzes data and returns answer

4. **Backend returns:**
   ```
   {
     "answer": "The payment service has 3 replicas, 1 has a CrashLoopBackOff...",
     "status": "ok",
     "confidence": 0.92,
     "sources": ["k8fy.live-state"]
   }
   ```

## Next Steps

The MVP integration above is done. Forward-looking work is tracked in the
context-mesh, not here:

1. **Two-tier query path** — deterministic fast-path + agentic synthesis
   ([ADR 0006](../context-mesh/decisions/0006-two-tier-query-path.md)). *Priority.*
2. **Everything else** — prioritized in [ROADMAP](../context-mesh/ROADMAP.md)
   (egress/redaction, storage consolidation, multi-tenancy, audit, observability,
   ML signals, correlation, supporting tooling).
