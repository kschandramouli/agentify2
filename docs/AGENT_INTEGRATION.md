# Agent Integration Guide

## Overview

The agent integration connects the backend's query orchestrator to Claude AI for intelligent reasoning about operational data. When a user asks a question, the system:

1. Parses the user's natural language question into an intent
2. Routes the query to relevant pods in the context-mesh
3. Fetches raw data from those pods
4. Sends the data to the Python agent service
5. Claude reasons about the data and returns a human-readable answer

## Architecture

### Components

**Backend Query Handler** (`src/backend/internal/api/handlers.go`)
- Receives HTTP POST to `/api/query`
- Parses question and context
- Orchestrates the query flow

**Intent Inference** (`inferIntent()`)
- Converts natural language question → query intent
- Current heuristics: "health" → health_check, "certificate" → cert_check, etc.
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
- `/reason` endpoint: receives data and intent, calls Claude API
- Returns answer, confidence, sources, reasoning

### Data Flow

```
User Question
    ↓
[Query Handler] → Parse question & context
    ↓
[Infer Intent] → Determine query type (health_check, cert_check, etc.)
    ↓
[Route to Pods] → Find relevant pods in registry based on intent
    ↓
[Fetch Data] → Query each pod's storage backend (Postgres, Redis, Weaviate)
    ↓
[Agent Client] → Send {question, intent, pod_data, context} to agent service
    ↓
[Claude API] → Reason about data using system prompt + tools
    ↓
[Format Response] → Return {answer, confidence, sources}
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
ANTHROPIC_API_KEY=sk-...                   # Claude API key
CLAUDE_MODEL=claude-opus-4-7               # Model to use
CLAUDE_MAX_TOKENS=2048                     # Response size limit
```

## Usage Example

### Request

```bash
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "question": "Is the payment service healthy in production?",
    "context": {
      "namespace": "prod"
    }
  }'
```

### Response

```json
{
  "answer": "The payment service is running with 3 replicas. Pod payment-svc-abc has 5 restarts (CrashLoopBackOff). The database connection appears to be failing. I recommend checking database connectivity and reviewing recent deployments.",
  "status": "ok",
  "confidence": 0.92,
  "sources": [
    "k8fy.live-state#payment-svc",
    "k8fy.events#payment-svc"
  ]
}
```

## Intent Types

The system currently recognizes these intents:

| Intent | Trigger Words | Pods Queried |
|--------|---------------|------------|
| `health_check` | "health", "healthy", "status" | k8fy.live-state shards |
| `cert_check` | "certificate", "cert", "expir" | k8fy.certificates |
| `metrics_query` | "metric", "cpu", "memory" | k8fy.metrics |
| `general_query` | (default) | all pods in namespace |

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
cd src/agent && python -m uvicorn main:app --port 8001

# Terminal 3: Send query
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "question": "Is payment service healthy?",
    "context": {"namespace": "prod"}
  }' | jq .
```

### Integration Test

```bash
make test-up        # Start test services
make test-integration  # Run tests (includes query testing)
```

## Future Improvements

1. **Tool Calling**: Enable Claude to invoke tools that query pods dynamically
2. **Multi-turn Conversation**: Keep chat history for follow-up questions
3. **Prompt Caching**: Cache system prompt to reduce API costs
4. **Dynamic Intent**: Let Claude infer intent from question instead of heuristics
5. **Correlation Queries**: Handle questions that span multiple data sources
6. **Scheduled Queries**: Support "alert me if X becomes unhealthy"

## Troubleshooting

**Agent service not responding:**
- Check agent service is running: `curl http://localhost:8001/health`
- Check backend logs for agent client errors
- Verify ANTHROPIC_API_KEY is set in agent service environment

**Query returns "Not implemented":**
- Agent service likely errored; check its logs
- Backend will fallback to returning raw data

**Slow responses:**
- Claude API might be slow; add timeout monitoring
- Consider enabling prompt caching to reduce latency
