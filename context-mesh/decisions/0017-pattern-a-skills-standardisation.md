# 0017 – Pattern A standardisation across all skill classes

## Status

Accepted · 2026-06-11

## Context

After spec 010 shipped the skill router (Pattern B: each intent dispatches to a
focused `K8fyAgent` sub-class with a specialised system prompt and a trimmed tool
subset), two execution strategies co-existed:

| Skill | Strategy before this ADR |
|-------|--------------------------|
| `HealthSkill` | Pattern A (pre-fetch + single Claude call) — already done |
| `CertAuditSkill` | Pattern A — already done |
| `ChangeHistorySkill` | Agentic loop (inherits base `K8fyAgent.reason()`) |
| `RestartTrendSkill` | Agentic loop (inherits base `K8fyAgent.reason()`) |
| `DiagnoseSkill` | Advisor/executor (`advisor_20260301` server-side tool: Sonnet executor + Opus advisor) |

The agentic loop for `ChangeHistorySkill` and `RestartTrendSkill` was unnecessary:
both intents have exactly one predictable tool call whose parameters are fully known
from `context` at dispatch time. Letting Claude decide whether to call the tool added
one extra round-trip with zero information gain.

`DiagnoseSkill`'s advisor/executor strategy was more complex: the server-side
`advisor_20260301` beta tool pairs two models in one API call. It was chosen to
approximate Opus-solo quality at Sonnet rates. In practice it introduced four
problems:
1. **Unpredictable cost**: 2–7 tool iterations per request depending on what the
   executor decided to explore.
2. **Unpredictable latency**: each iteration is a sequential round-trip.
3. **Beta surface dependency**: `advisor_20260301` is a non-GA server-side tool
   subject to change or removal without notice.
4. **Coverage gaps**: the executor occasionally skipped a signal (e.g., logs) that
   Pattern A unconditionally fetches.

All five signals needed for diagnosis (`service_health`, `pod_events`,
`metrics_history`, `change_history`, `pod_logs` for crashing pods) are derivable
from `data` and `context` alone before the Claude call is made.

## Decision

Standardise **all five skill classes on Pattern A**: deterministic parallel pre-fetch
followed by a single Claude call with all data assembled.

### ChangeHistorySkill
Pre-fetch: `get_change_history(namespace, service_name)` — unconditionally.
Result key: `change_history`.
Claude model: `settings.claude_model` (Sonnet by default).

### RestartTrendSkill
Pre-fetch: `get_metrics_history(namespace, service_name, order=asc)` — unconditionally.
Result key: `metrics_history`.
Claude model: `settings.claude_model`.

### DiagnoseSkill
Pre-fetch (all parallel via `asyncio.gather`):
1. `get_service_health(service_name, namespace)` — always when `service_name` known.
2. `get_pod_events(pod_id, namespace)` — for every pod in initial `data`.
3. `get_metrics_history(namespace, service_name, order=asc)` — always.
4. `get_change_history(namespace, service_name)` — always.
5. `get_logs(namespace, pod, previous=True)` — only for pods with
   `restarts >= 3` or `phase in {Failed, Unknown, CrashLoopBackOff}`. Routes
   to the Glue/Athena log platform first when configured (ADR 0021), else
   the live cluster — see `log_router.py`.
Result keys: `service_health`, `events.<pod-id>`, `metrics_history`, `change_history`,
`logs.<pod-id>`.
Claude model: overridden to `ADVISOR_MODEL` (`claude-opus-4-8`) — diagnosis warrants
the most capable model; we lose the Sonnet-rates benefit of advisor/executor but gain
determinism and full signal coverage.

### Shared pattern (all five skills)
```python
async def reason(self, intent, data, context):
    prefetched = await self._prefetch(data, context)          # deterministic
    return await self._reason_pattern_a(intent, data, context, prefetched)  # 1 call
```

No tools are declared in the `messages.create` call; pre-fetched data is injected
into the user message by `_reason_pattern_a`. Prompt instructions updated to reflect
"data is pre-fetched in `<key>` — no tool call needed."

Pre-fetch failures are caught per-key, logged as warnings, and silently excluded from
the payload so a network error on one signal doesn't abort the whole request.

## Consequences

- **Positive:**
  - All five skill costs are now **O(1 Claude call)** — fully predictable.
  - Latency is bounded: pre-fetch is a single `asyncio.gather` round; the Claude call
    is the only remaining variable.
  - The `advisor_20260301` beta surface is eliminated — no non-GA dependency.
  - `DiagnoseSkill` now unconditionally fetches crash logs for crashing pods (the
    agentic loop occasionally skipped them).
  - Prompts are cleaner: tool-calling instructions removed from all five skill prompts.

- **Negative / cost accepted:**
  - `DiagnoseSkill` now uses Opus (`claude-opus-4-8`) for every diagnosis call rather
    than Sonnet for the agentic bulk. Per-call cost is higher; per-answer quality is
    deterministically Opus-level.
  - Pre-fetch always fires all signals even when some turn out to be empty — a small
    amount of wasted backend round-trips for healthy services.

- **Revisit if:**
  - A new intent is introduced where the required tool calls cannot be determined from
    `data`/`context` before the Claude call (open-ended exploration) — that intent
    belongs in Pattern B, not Pattern A.
  - The `advisor_20260301` tool becomes GA and materially outperforms Pattern A on
    diagnostic quality at lower cost.
