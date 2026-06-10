# Agent Integration Guide

## Overview

The agent integration connects the backend's query orchestrator to Claude AI for intelligent reasoning about operational data. When a user asks a question, the system:

1. Parses the user's natural language question into an intent
2. Routes the query to relevant pods in the context-mesh
3. Fetches raw data from those pods
4. Sends the data to the Python agent service
5. The Skill Router dispatches the intent to the narrowest skill that can handle it
6. Claude reasons about the data using a skill-specific prompt and tool subset
7. Returns a structured, human-readable answer

## Architecture

### Components

**Backend Query Handler** (`src/backend/internal/api/handlers.go`)
- Receives HTTP POST to `/api/query`
- Parses question and context
- Orchestrates the query flow

**Intent Inference** (`inferIntent()`)
- Converts natural language question → query intent
- Current heuristics: "health" → `health_check`, "certificate" → `cert_check`, "diagnose/why/cause" → `diagnose`, etc.
- TODO: Replace with Claude-based NLP in future

**Query Executor** (`src/backend/internal/orchestrator/query_executor.go`)
- Routes queries to pods based on intent
- Fetches data from selected pods
- Correlates results from multiple pods

**Agent Client** (`src/backend/internal/api/agent_client.go`)
- HTTP client that calls the Python agent service
- Sends question, intent, raw pod data, and context
- Receives structured response from Claude

**Python Agent Service** (`src/agent/`)
- FastAPI service running on `:8001`
- `/reason` endpoint: receives data and intent, routes to Skill Router
- Returns structured `AgentResponse` (answer, status, confidence, sources, details)

**Skill Router** (`src/agent/k8fy/skills/router.py`)
- Dispatches each intent to the narrowest skill sub-agent (spec 010 — Pattern B)
- O(1) dispatch table — intent is pre-classified by Go; no classification cost here
- Falls back to the full `K8fyAgent` for unrecognised intents

### Skills

| Skill | Intent | Tool subset | Strategy |
|-------|--------|-------------|----------|
| `HealthSkill` | `health_check` | `get_service_health`, `query_pod`, `get_pod_events` | **Pattern A** — parallel pre-fetch + 1 Claude call |
| `CertAuditSkill` | `cert_check` | `get_certificates` | **Pattern A** — pre-fetch certs + 1 Claude call |
| `DiagnoseSkill` | `diagnose` | 6 tools (all except `get_certificates`) | **Pattern B** — agentic loop, Opus advisor + Sonnet executor |
| `K8fyAgent` (fallback) | `general_query`, `metrics_query`, anything else | All 7 tools | **Pattern B** — agentic loop, single model |

`DiagnoseSkill` excludes `get_certificates` because cert data arrives in the pre-fetched payload for `diagnose` queries — the agent reads it from context without a redundant tool call.

### Advisor/Executor Strategy (DiagnoseSkill)

`DiagnoseSkill` uses the Claude `advisor_20260301` server-side tool to pair two models in a single API call:

- **Executor — Sonnet 4.6** (`claude-sonnet-4-6`): primary model; handles all K8fy tool calls cheaply
- **Advisor — Opus 4.8** (`claude-opus-4-8`): consulted mid-generation via the server-side tool for strategic diagnostic planning

The executor calls the advisor tool when committing to a diagnostic approach, when tool results are ambiguous, and before producing the final answer. Advisor output is capped at 2,048 tokens per call; up to 3 advisor calls are allowed per request. All of this happens inside a single `/v1/messages` call — no extra round trips.

### Data Flow

```
User Question
    ↓
[Query Handler] → Parse question & context
    ↓
[Infer Intent] → Determine query type (health_check, cert_check, diagnose, …)
    ↓
[Route to Pods] → Find relevant pods in registry based on intent
    ↓
[Fetch Data] → Query each pod's storage backend (Postgres, Redis, Weaviate)
    ↓
[Agent Client] → Send {question, intent, pod_data, context} to agent service
    ↓
[Skill Router] → Dispatch intent → HealthSkill | CertAuditSkill | DiagnoseSkill | K8fyAgent
    ↓
[Claude API] → Reason about data using skill-specific prompt + tool subset
              (DiagnoseSkill: Sonnet executor + Opus advisor tool)
    ↓
[Format Response] → Return {answer, status, confidence, sources, details}
    ↓
User Answer
```

## Configuration

**Backend** (via environment variables):
```bash
AGENT_SERVICE_URL=http://localhost:8001   # Where agent service is running
```

**Agent Service** (via `.env` or environment):
```bash
ANTHROPIC_API_KEY=sk-...                  # Claude API key (also accepted as CLAUDE_API_KEY)
CLAUDE_MODEL=claude-opus-4-8              # Default model (single-model path)
CLAUDE_MAX_TOKENS=4096                    # Room for adaptive thinking + structured answer
CLAUDE_EFFORT=high                        # low | medium | high | max
AGENT_MAX_TOOL_ITERATIONS=5              # Cap on the tool-calling loop per request
```

DiagnoseSkill's advisor (`claude-opus-4-8`) and executor (`claude-sonnet-4-6`) models are hardcoded constants in `src/agent/k8fy/agent.py` (`ADVISOR_MODEL`, `EXECUTOR_MODEL`).

## Usage Example

### Request

```bash
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "question": "Why is the payment service crashing in production?",
    "context": {
      "namespace": "prod"
    }
  }'
```

### Response

```json
{
  "answer": "payment-svc is in CrashLoopBackOff. Logs show 'OOMKilled' on the previous container instance. Restarts climbed from 0 to 17 between 14:08 and 14:31, roughly 3 minutes after revision 7 rolled out — likely trigger. Recommend increasing the memory limit and rolling back revision 7 to confirm.",
  "status": "unhealthy",
  "confidence": 0.91,
  "sources": [
    "k8fy.live-state",
    "k8fy.events",
    "k8fy.metrics"
  ],
  "details": {
    "findings": [
      "payment-svc: CrashLoopBackOff, 17 restarts",
      "Logs: OOMKilled on previous container",
      "Metrics: restart ramp 14:08–14:31",
      "Change event: revision 7 deployed 14:05"
    ],
    "likely_cause": "Memory limit too low; OOMKill triggered after revision 7 rollout",
    "severity": "critical",
    "recommendations": [
      "Increase memory limit for payment-svc",
      "Roll back revision 7 to confirm trigger",
      "Review memory allocation in new revision"
    ]
  }
}
```

## Intent Types

The system currently recognises these intents:

| Intent | Trigger words (heuristic) | Skill dispatched | Tools available |
|--------|--------------------------|------------------|-----------------|
| `health_check` | "health", "healthy", "status", "up" | `HealthSkill` | 3 |
| `cert_check` | "certificate", "cert", "expir", "tls" | `CertAuditSkill` | 1 |
| `diagnose` | "diagnose", "why", "cause", "failing", "crash" | `DiagnoseSkill` | 6 |
| `metrics_query` | "metric", "cpu", "memory", "usage" | `K8fyAgent` (fallback) | 7 |
| `general_query` | (default) | `K8fyAgent` (fallback) | 7 |

## Tools

The agent has access to 7 tools that fetch live data from the backend via `POST /api/agent/fetch`:

| Tool | Description | Key parameters |
|------|-------------|----------------|
| `get_service_health` | Endpoints, ready ratio, pod statuses for a service | `service_name`, `namespace` |
| `query_pod` | Phase, ready status, restart count for a specific pod | `pod_id`, `namespace` |
| `get_pod_events` | Recent warning/crash events for a pod | `pod_id`, `namespace`, `limit` |
| `get_certificates` | Certificate list, expiry dates, renewal needs | `namespace` (optional) |
| `get_pod_logs` | Bounded redacted log tail — use `previous=true` for the crashed container | `pod_id`, `namespace`, `previous`, `tail_lines` |
| `get_metrics_history` | Restart-count time-series over a window | `pod_id`, `namespace`, `since`, `until`, `order` |
| `get_change_history` | Deployment/rollout events over a time window | `deployment`, `namespace`, `since`, `until` |

Tools are defined in `src/agent/k8fy/tools.py`. Each skill sub-agent is given only the tools relevant to its domain (see Skills table above).

## Fallback Behavior

If the agent service is unavailable:
- Backend returns raw pod data formatted as plain text
- Response has `status: "partial"` and `confidence: 0.5`
- User gets data but without Claude's reasoning

## Testing

### Manual Test

Start services:
```bash
# Terminal 1: Start backend
cd src/backend && go run cmd/agentify/main.go

# Terminal 2: Start agent service
cd src/agent && python -m uvicorn app:app --port 8001

# Terminal 3: Send a health query
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "question": "Is payment service healthy?",
    "context": {"namespace": "prod"}
  }' | jq .

# Terminal 3: Send a diagnose query (uses advisor/executor path)
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "question": "Why is the payment service crashing?",
    "context": {"namespace": "prod"}
  }' | jq .
```

### Integration Test

```bash
make test-up            # Start test services
make test-integration   # Run tests (includes query testing)
```

## Future Improvements

1. **Multi-turn Conversation**: Keep chat history for follow-up questions
2. **Dynamic Intent**: Let Claude infer intent from question instead of heuristics
3. **Correlation Queries**: Handle questions that span multiple data sources
4. **Scheduled Queries**: Support "alert me if X becomes unhealthy"

## Troubleshooting

**Agent service not responding:**
- Check agent service is running: `curl http://localhost:8001/health`
- Check backend logs for agent client errors
- Verify `ANTHROPIC_API_KEY` is set in agent service environment

**Query returns "Not implemented":**
- Agent service likely errored; check its logs
- Backend will fall back to returning raw data

**Slow responses on diagnose queries:**
- DiagnoseSkill uses the `advisor_20260301` beta tool; the advisor sub-inference does not stream — expect a short pause while Opus runs
- Check `usage.iterations` in logs for advisor token counts; advisor tokens are billed separately at Opus rates
