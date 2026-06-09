# Sequence Flows

End-to-end call sequences for every query path through the system.
Diagrams are in [Mermaid](https://mermaid.js.org) — rendered by GitHub, VS Code
(Mermaid Preview extension), and most documentation tools.

**Legend**
- **Solid arrow** (`->>`) — explicit code call / HTTP request
- **Dashed arrow** (`-->>`) — return / response
- **Dashed box** (`rect`) — server-side or parallel boundary
- `[Tier-1]` — deterministic, no LLM
- `[Tier-2]` — agentic, involves Claude
- `[Pattern A]` — parallel pre-fetch + single Claude call
- `[Pattern B]` — Claude-driven agentic tool loop

---

## 1. End-to-end overview — all paths

Shows the Tier-1 fast-path, the Tier-2 skill dispatch, and which strategy each
skill uses. Expand the per-skill diagrams below for internal detail.

```mermaid
sequenceDiagram
    actor Operator
    participant FE   as Frontend
    participant Go   as Go Backend
    participant Py   as Agent Service (Python)

    Operator->>FE: ask question
    FE->>Go: POST /api/query {question, context}

    Go->>Go: inferIntent() → intent label

    Note over Go: [Tier-1] Deterministic fast-path
    Go->>Go: tryDeterministic(intent, data)

    alt Tier-1 answer available (simple health / cert rule)
        Note over Go: 0 LLM calls · <1 ms · confidence = 1.0
        Go-->>FE: AgentResponse
    else Tier-2 required
        Go->>Go: fetchPodData() — parallel pod queries
        Go->>Py: POST /reason {intent, data, context}

        Note over Py: SkillRouter.dispatch(intent)

        alt health_check → HealthSkill
            Note over Py: [Pattern A] parallel pre-fetch → 1 Opus call
        else cert_check → CertAuditSkill
            Note over Py: [Pattern A] 1 cert fetch → 1 Opus call
        else diagnose → DiagnoseSkill
            Note over Py: [Pattern B] Sonnet executor + Opus advisor loop
        else general_query / metrics_query → K8fyAgent
            Note over Py: [Pattern B] Opus agentic loop · up to 5 tool iterations
        end

        Py-->>Go: AgentResponse {answer, status, confidence, sources, details}
        Go-->>FE: formatted response
    end

    FE-->>Operator: answer · status badge · sources · trace_id
```

---

## 2. Pattern A — HealthSkill (`health_check`)

Pre-fetches service health and degraded-pod events in parallel, then makes
exactly one Claude call. No agentic tool loop.

```mermaid
sequenceDiagram
    participant Router  as SkillRouter
    participant Skill   as HealthSkill
    participant BE      as Backend API<br/>/api/agent/fetch
    participant Anthropic as Anthropic API<br/>claude-opus-4-8

    Router->>Skill: dispatch("health_check", data, context)

    Note over Skill: [Pattern A] derive what to fetch from data + context

    par Pre-fetch in parallel (asyncio.gather)
        Skill->>BE: get_service_health(service_name, namespace)
        BE-->>Skill: endpoints · ready ratio · pod statuses
    and
        Skill->>BE: get_pod_events(pod_id, namespace)<br/>⚠ only for pods with restarts > 0 or ready=False
        BE-->>Skill: warning / crash events
    end

    Note over Skill: merge pre-fetched data into user message

    Skill->>Anthropic: messages.create<br/>model=opus-4-8 · no tools · merged data
    Note over Anthropic: adaptive thinking · effort=high<br/>structured output (REASONING_SCHEMA)
    Anthropic-->>Skill: {answer, status, confidence, …}

    Skill-->>Router: AgentResponse
```

**Cost profile:** N parallel backend fetches (milliseconds, no billing) + **1 Opus call**.
Tool iterations recorded in Prometheus: **0**.

---

## 3. Pattern A — CertAuditSkill (`cert_check`)

The simplest Pattern A case. The cert list is the only data source and its
parameters are fully known from context — always one fetch, always one call.

```mermaid
sequenceDiagram
    participant Router  as SkillRouter
    participant Skill   as CertAuditSkill
    participant BE      as Backend API<br/>/api/agent/fetch
    participant Anthropic as Anthropic API<br/>claude-opus-4-8

    Router->>Skill: dispatch("cert_check", data, context)

    Skill->>BE: get_certificates(namespace)
    BE-->>Skill: cert list · expiry dates

    Note over Skill: merge certs into user message

    Skill->>Anthropic: messages.create<br/>model=opus-4-8 · no tools · data + certs
    Note over Anthropic: adaptive thinking · effort=high<br/>structured output (REASONING_SCHEMA)
    Anthropic-->>Skill: {answer, status, confidence, …}

    Skill-->>Router: AgentResponse
```

**Cost profile:** 1 backend fetch + **1 Opus call**.
Tool iterations recorded in Prometheus: **0**.

---

## 4. Pattern B — DiagnoseSkill (`diagnose`) with Advisor/Executor

The executor (Sonnet 4.6) is the primary model and drives the tool loop.
The advisor (Opus 4.8) is a server-side tool the executor calls for strategic
guidance — all within a single `beta.messages.create` request per iteration.

```mermaid
sequenceDiagram
    participant Router   as SkillRouter
    participant Skill    as DiagnoseSkill
    participant BE       as Backend API<br/>/api/agent/fetch
    participant Server   as Anthropic Server
    participant Sonnet   as Sonnet 4.6<br/>(executor · primary)
    participant Opus     as Opus 4.8<br/>(advisor · server-side)

    Router->>Skill: dispatch("diagnose", data, context)

    Note over Skill: [Pattern B] single beta.messages.create per loop iteration

    loop Agentic tool loop (up to max_iterations)
        Skill->>Server: beta.messages.create<br/>model=sonnet-4-6 · tools=[advisor, get_service_health, …]
        activate Server

        Note over Server,Sonnet: Executor turn begins

        rect rgb(240, 248, 255)
            Note over Server,Opus: [Server-side advisor sub-inference — no client round-trip]
            Server->>Opus: advisor tool call<br/>full conversation forwarded automatically
            Note over Opus: adaptive thinking · max_tokens=2048<br/>produces diagnostic plan / course correction
            Opus-->>Server: advisor_tool_result (plan text)
        end

        Server-->>Skill: response with server_tool_use + advisor_tool_result + tool_use blocks
        deactivate Server

        alt stop_reason == tool_use  (K8fy tool calls needed)
            loop For each tool_use block
                Skill->>BE: /api/agent/fetch {tool, args}
                BE-->>Skill: tool result
            end
            Note over Skill: append assistant turn + tool_results to messages
        else stop_reason == end_turn
            Note over Skill: final answer reached
        end
    end

    Skill-->>Router: AgentResponse {findings, likely_cause, severity, …}
```

**Cost profile:** per iteration — Sonnet tokens (executor) + Opus tokens (advisor, billed separately via `usage.iterations`).
Advisor called up to **3 times** per request (`max_uses=3`). Tool iterations recorded in Prometheus: **actual count**.

---

## 5. Pattern B — K8fyAgent fallback (`general_query`, `metrics_query`)

Single-model agentic loop. No advisor tool. Claude decides which tools to call
based on the data and question.

```mermaid
sequenceDiagram
    participant Router  as SkillRouter
    participant Agent   as K8fyAgent (fallback)
    participant BE      as Backend API<br/>/api/agent/fetch
    participant Anthropic as Anthropic API<br/>claude-opus-4-8

    Router->>Agent: dispatch("general_query", data, context)

    Note over Agent: [Pattern B] single-model agentic loop

    loop Agentic tool loop (up to max_iterations = 5)
        Agent->>Anthropic: messages.create<br/>model=opus-4-8 · all 7 tools · accumulated messages
        Note over Anthropic: adaptive thinking · effort=high<br/>structured output (REASONING_SCHEMA)

        alt stop_reason == tool_use
            Anthropic-->>Agent: tool_use blocks
            loop For each tool_use block
                Agent->>BE: /api/agent/fetch {tool, args}
                BE-->>Agent: tool result
            end
            Note over Agent: append assistant turn + tool_results
        else stop_reason == end_turn
            Anthropic-->>Agent: final structured JSON
            Note over Agent: loop exits
        end
    end

    Agent-->>Router: AgentResponse
```

**Cost profile:** N Opus calls (one per loop iteration). Tool iterations recorded in Prometheus: **actual count**.

---

## Summary comparison

| Path | Skill | LLM calls | Tool loop | Models |
|------|-------|-----------|-----------|--------|
| Tier-1 | — | **0** | no | — |
| Pattern A | HealthSkill | **1** | no | Opus 4.8 |
| Pattern A | CertAuditSkill | **1** | no | Opus 4.8 |
| Pattern B | DiagnoseSkill | **1–N** (Sonnet) + advisor sub-calls (Opus) | yes | Sonnet 4.6 + Opus 4.8 |
| Pattern B | K8fyAgent | **1–N** (Opus) | yes | Opus 4.8 |
