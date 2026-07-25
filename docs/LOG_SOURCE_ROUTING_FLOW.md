# Log source routing: frontend → backend → agent → Athena/cluster

How a question about a service turns into the right log source being read,
with no manual configuration (ADR 0021 / P15 connector, phase 1). Covers both
paths that fetch logs: the free-form **Chat** path and the deterministic
**Diagnose** (Tier-2) path.

## Component interaction

```mermaid
flowchart TD
    subgraph FE["Frontend (React)"]
        UI["ChatPanel.tsx<br/>operator asks about a service"]
    end

    subgraph BE["Backend (Go)"]
        API["HandleSendChatMessage<br/>POST /api/chat/sessions/:id/messages"]
        AC["AgentClient.Chat()<br/>POST agent:8001/reason-chat"]
        API --> AC
    end

    subgraph AG["Agent (Python)"]
        RC["reason_chat()<br/>free-form tool-calling loop"]
        PTC["process_tool_call()<br/>tools.py"]
        GL["get_logs() router<br/>log_router.py"]
        DS["DiagnoseSkill._prefetch()<br/>skills/diagnose.py — Tier-2 only"]
        RC -- "model calls get_logs tool" --> PTC
        PTC --> GL
        DS -- "direct call, no model in the loop" --> GL
    end

    subgraph SRC["Log sources"]
        ATH["Glue/Athena test harness<br/>log_platform.py (boto3)"]
        K8S["Live Kubernetes API<br/>live_diagnostics.py (in-cluster SA token)"]
    end

    UI -->|"sendChatMessage()"| API
    AC --> RC
    GL -->|"1. try first, if configured"| ATH
    GL -->|"2. fall back on empty/error"| K8S
    ATH -.->|rows or empty/error| GL
    K8S -.->|log lines| GL
    GL -.->|"{namespace, pod, logs}"| PTC
    GL -.->|"{namespace, pod, logs}"| DS
    PTC -.-> RC
    RC -.->|answer + recommended_actions| AC
    AC -.-> API
    API -.->|structured ChatMessage| UI
```

## Where "discovery" actually happens

There is no registry to look up and no per-namespace flag to set. Discovery
is a runtime decision made once, inside `log_router.get_logs()`, every time
logs are requested:

```mermaid
flowchart LR
    Start(["get_logs(namespace, pod)"]) --> Cfg{"Agent settings has\nATHENA_WORKGROUP/DATABASE/TABLE?"}
    Cfg -- "no (unconfigured)" --> Cluster["live_get_pod_logs()\n(live K8s API)"]
    Cfg -- "yes" --> Query["query_athena_logs()\n(boto3: start → poll → fetch)"]
    Query --> Result{"result?"}
    Result -- "rows found" --> UseAthena["return Athena logs\n(durable, retains history)"]
    Result -- "empty (never onboarded\nto Firehose) or error" --> Cluster
    Cluster --> Return(["{namespace, pod, logs}"])
    UseAthena --> Return
```

Same function, same output shape, either caller:

- **Chat path** — `get_logs` is a Claude-callable tool. The model just asks
  for logs; which backend answered is invisible to it (`tools.py`
  registers/dispatches it, `prompts.py` tells Claude to prefer it over
  `get_pod_logs`/`live_get_pod_logs`).
- **Diagnose path** — `DiagnoseSkill._prefetch()` (Pattern A: parallel
  `asyncio.gather` pre-fetch, then exactly one Opus call with no `tools=`
  passed) calls `get_logs()` directly as plain Python, for every pod that
  looks like it's crashing.
- **Incident-response path** — `IncidentResponderSkill._prefetch()` (same
  Pattern A shape, spec 011 Use Case 1) also calls `get_logs()` directly when
  building a remediation proposal.

## End-to-end data flow (chat path, sequence)

```mermaid
sequenceDiagram
    participant U as Operator (browser)
    participant FE as ChatPanel.tsx
    participant BE as Go backend
    participant AG as Python agent (reason_chat)
    participant LR as log_router.get_logs
    participant ATH as Athena/Glue
    participant K8S as Live K8s API

    U->>FE: "why is payment-worker crashing?"
    FE->>BE: POST /api/chat/sessions/:id/messages
    BE->>AG: POST /reason-chat {messages, context}
    AG->>AG: Claude decides to call get_logs tool
    AG->>LR: get_logs(namespace="payments", pod="payment-worker-abc", previous=true)
    LR->>ATH: query_athena_logs() [if ATHENA_* configured]
    alt Athena has rows
        ATH-->>LR: redacted log lines
    else empty or error
        ATH-->>LR: "" or {error}
        LR->>K8S: live_get_pod_logs()
        K8S-->>LR: redacted log lines
    end
    LR-->>AG: {namespace, pod, logs}
    AG->>AG: Claude reads logs, finds crash reason
    AG->>AG: _structure_chat_answer() (2nd Claude call, schema-constrained)
    AG-->>BE: AgentResponse {answer, findings, recommended_actions, ...}
    BE-->>FE: structured ChatMessage
    FE-->>U: sectioned answer + "Run" buttons for recommended actions
```

## Key files

| Layer | File | Role |
|---|---|---|
| Frontend | `src/frontend/src/components/ChatPanel.tsx` | operator's chat UI |
| Frontend | `src/frontend/src/api.ts` | `sendChatMessage()` |
| Backend | `src/backend/internal/api/handlers.go` | `HandleSendChatMessage` |
| Backend | `src/backend/internal/api/agent_client.go` | `AgentClient.Chat()` → `POST /reason-chat` |
| Agent | `src/agent/k8fy/agent.py` | `reason_chat()` — free-form tool loop |
| Agent | `src/agent/k8fy/skills/diagnose.py` | `DiagnoseSkill._prefetch()` — Tier-2 deterministic prefetch |
| Agent | `src/agent/k8fy/tools.py` | `get_logs` tool registration + dispatch |
| Agent | `src/agent/k8fy/log_router.py` | **the routing decision** — Athena first, cluster fallback |
| Agent | `src/agent/k8fy/log_platform.py` | Athena/Glue query (boto3) |
| Agent | `src/agent/k8fy/live_diagnostics.py` | live K8s API query |
| Agent | `src/agent/k8fy/log_redaction.py` | shared secret-scrubbing, used by both sources |
| Agent | `src/agent/config/settings.py` | `ATHENA_WORKGROUP/DATABASE/TABLE` |
| Infra | `infra/kubernetes/agent.yaml` | Athena connection env vars (agent pod only) |
| Infra | `infra/terraform/aws/logging.tf` | Fargate→Firehose→S3→Glue pipeline (ADR 0021), IRSA grants |
