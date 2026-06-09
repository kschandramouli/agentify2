# 010 – Skill router (Pattern A + Pattern B: specialised sub-agents per intent)

> Builds on [ADR 0006](../decisions/0006-two-tier-query-path.md) (Tier-2 agentic
> path), [spec 005](005-root-cause-correlation.md) (diagnose intent), and the P5
> skills design recorded in [ROADMAP.md](../ROADMAP.md#p5--supporting-tooling-when-scaling).

## Goal

Replace the single monolithic `K8fyAgent` (all tools, one general prompt) with a
lightweight skill router that dispatches each intent to a focused sub-agent, reducing
token cost and improving answer quality by giving each skill only the tools and prompt
context relevant to its domain.

## Depends on

- Policies: [data-governance](../policies/data-governance.md) (redaction happens
  upstream in Go before the agent sees data — unchanged).
- Specs: [003](003-tier1-deterministic-queries.md) (Tier-1 exits before the router
  runs), [005](005-root-cause-correlation.md) (diagnose intent + structured output
  schema is preserved as-is).

## Context / constraints

- The intent is **already classified** by `inferIntent()` in Go (`handlers.go`)
  before the HTTP call reaches the Python agent — the router is a pure dispatch
  table, zero extra classification cost.
- The Go backend, the `/reason` HTTP contract, and `AgentResponse`/`QueryRequest`
  Pydantic models are **untouched** — the router is Python-layer only.
- The `REASONING_SCHEMA` (structured JSON output) and the agentic tool-call loop in
  `K8fyAgent.reason()` are **unchanged** — skills reuse that loop with a narrower
  context (different prompt + tool subset).
- `K8fyAgent` becomes the fallback for unrecognised intents (`general_query`,
  `metrics_query`) — all 7 tools, full prompt, existing behaviour preserved exactly.
- **Non-goals:** Pattern A pre-fetch optimisation (one Claude call, pre-assembled
  data) is a follow-up; this spec covers Pattern B routing only. No new HTTP
  endpoints. No changes to Go.

## Skills

| Intent | Skill class | Tool subset | Prompt focus | Model strategy |
|--------|-------------|-------------|--------------|----------------|
| `health_check` | `HealthSkill` | `get_service_health`, `query_pod`, `get_pod_events` | K8s health model expert | Single model |
| `cert_check` | `CertAuditSkill` | `get_certificates` | PKI/TLS lifecycle expert | Single model |
| `diagnose` | `DiagnoseSkill` | all except `get_certificates` (6 tools) | Failure-mode + causal correlation | Advisor/executor (see below) |
| anything else | `K8fyAgent` (fallback) | all 7 tools | Existing general prompt | Single model |

`DiagnoseSkill` deliberately excludes `get_certificates` from its tool set: cert
data already arrives in the pre-fetched `data` payload (fan-out routes to cert pods
for `diagnose`), so the agent reads it from context without needing a tool call.

### Pattern A strategy (HealthSkill, CertAuditSkill)

`HealthSkill` and `CertAuditSkill` use Pattern A: the skill derives exactly
which tool calls are needed from `data` and `context` alone, fires them in
parallel with `asyncio.gather`, then makes a single Claude call with the
assembled result. No tools are declared in the Claude request; the pre-fetched
data is injected directly into the user message.

Pre-fetch sequences:
- `HealthSkill`: `get_service_health` (when `service_name` is in context) +
  `get_pod_events` for every pod in initial data with `restarts > 0` or
  `ready=False`. These are the only two tool calls the agentic loop would have
  made anyway — pre-fetching eliminates the round-trip.
- `CertAuditSkill`: `get_certificates(namespace)` — unconditionally, since
  cert data is always the single data source for this intent.

Cost profile per request: N parallel backend fetches + exactly 1 Claude call.

### Advisor/executor strategy (DiagnoseSkill)

`DiagnoseSkill` uses the `advisor_20260301` server-side tool to pair two models in a
single API call. The executor (`claude-sonnet-4-6`) is the primary model and handles
all tool calls; the advisor (`claude-opus-4-8`) is a server-side tool the executor
consults mid-generation for strategic guidance. This achieves close to Opus-solo
diagnostic quality while the bulk of token generation happens at Sonnet rates.

The advisor is called when the executor is committing to a diagnostic approach, when
signals conflict, and before producing the final answer. It is capped at 2,048 output
tokens and 3 calls per request. See `agent.py` constants `ADVISOR_MODEL` /
`EXECUTOR_MODEL` and `_reason_advisor_executor()` for the implementation.

## Behavior

- **Given** intent `health_check` **when** the router dispatches **then** only
  `get_service_health`, `query_pod`, `get_pod_events` are offered to Claude; the
  prompt contains only the health model definitions.
- **Given** intent `cert_check` **when** the router dispatches **then** only
  `get_certificates` is offered; the prompt is PKI-focused.
- **Given** intent `diagnose` **when** the router dispatches **then** 6 operational
  tools (no cert tool) are offered; the prompt contains crash/failure/temporal
  diagnosis guidance.
- **Given** any other intent **when** the router dispatches **then** the full
  `K8fyAgent` runs unchanged (backward-compatible fallback).
- **Given** an unknown intent not in the dispatch table **then** falls back to
  `K8fyAgent` — no error, no change in observable behaviour.

## Interfaces

```python
# src/agent/k8fy/skills/router.py
class SkillRouter:
    async def dispatch(intent: str, data: dict, context: dict) -> AgentResponse: ...

def get_skill_router() -> SkillRouter: ...   # singleton, thread-safe

# src/agent/k8fy/agent.py  (parameterised K8fyAgent)
class K8fyAgent:
    def __init__(
        self,
        system_prompt: str = SYSTEM_PROMPT,
        tools: list = TOOLS,
        advisor_model: str | None = None,   # None → single-model path
        executor_model: str | None = None,  # defaults to settings.claude_model
    ): ...

# src/agent/app.py  (one-line change)
# BEFORE: agent = get_k8fy_agent(); response = await agent.reason(...)
# AFTER:  response = await get_skill_router().dispatch(request.intent, ...)
```

## Acceptance criteria

- [ ] `health_check` query reaches `HealthSkill`; Claude is offered exactly 3 tools.
- [ ] `cert_check` query reaches `CertAuditSkill`; Claude is offered exactly 1 tool.
- [ ] `diagnose` query reaches `DiagnoseSkill`; Claude is offered exactly 6 tools.
- [ ] `general_query` / `metrics_query` fall through to `K8fyAgent` (all 7 tools).
- [ ] `AgentResponse` schema is identical for all skill paths — Go backend unchanged.
- [ ] Existing unit tests for `K8fyAgent.reason()` still pass (no breaking changes).
