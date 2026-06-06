# 003 – Tier-1 deterministic query fast-path

> Formalizes the routing/mechanics decided in
> [ADR 0006](../decisions/0006-two-tier-query-path.md) and already implemented.
> Written post-implementation to pin the contract; future changes lead here.
> The **health/cert rules themselves are owned by [spec 002](002-k8fy-health-queries.md)** —
> this spec covers *when* we answer deterministically vs. hand off to the agent.

## Goal

Answer deterministic structured queries (health, certs) directly from stored data
with **no LLM call** (single-digit-ms latency, zero API cost, stable output),
reserving the agent/LLM for synthesis, correlation, and free-text questions.

## Depends on

- Decision: [ADR 0006](../decisions/0006-two-tier-query-path.md) (two-tier path).
- Specs: [002-k8fy-health-queries](002-k8fy-health-queries.md) — **the single source
  of truth for the health model, thresholds, and renewal rule.** Any deterministic
  implementation (the Go evaluator) must match 002; it must not invent its own rules.
- Policies: [storage-strategy](../policies/storage-strategy.md) + ADR 0005 (the pod
  taxonomy that routing targets); [correlation](../policies/correlation.md) (Tier 2).

## Context / constraints

- **As built:** the rules live in Go at `internal/orchestrator/evaluator` (pure,
  unit-tested); the tier decision + answer assembly live in `internal/api/tier1.go`;
  `HandleQuery` runs Tier 1 after fetching pod data and falls through to the agent.
- The health model is **owned by spec 002**. The Go evaluator is the **sole
  implementation** of it. (The prior Python copy, `src/agent/k8fy/reasoning.py`, was
  removed 2026-06-01 — it was dead code, and keeping it risked silent drift.)
- **Non-goals (v1):**
  - Free-text entity resolution (mapping "payment" → specific pods) — Tier 1 answers
    at the namespace-shard aggregate level; naming a specific service is a Tier-2 strength.
  - `metrics_query` deterministic thresholds — routed to Tier 2 until thresholds are defined.
  - Detecting that a "health"-phrased question is really a causal "why" question —
    Tier 1 does not classify intent beyond the keyword inference; nuance goes to Tier 2.

## Tier decision

The orchestrator classifies intent (today: keyword inference in `inferIntent`) and
picks a tier:

| Intent | Tier | How answered |
|--------|------|--------------|
| `health_check` | 1 | pod-health rule per pod → service aggregate (spec 002) |
| `cert_check` | 1 | 30-day renewal rule per cert (spec 002) |
| `metrics_query` | 2 | agent (no deterministic threshold yet) |
| `general_query` / anything else | 2 | agent (synthesis / free-text) |

### Fall-through to Tier 2

Tier 1 returns "not handled" — and the query proceeds to the agent — when **any** of:
- the intent is not in the Tier-1 set above, **or**
- there are no evaluable rows in the fetched data (nothing to apply the rule to).

It does **not** currently fall through on compound/"why" phrasings (see Non-goals).

## Behavior

- **Given** a `health_check` for namespace `prod` **and** the shard holds 2 pods
  (1 Ready, 1 CrashLoopBackOff) **when** queried **then** return, with no LLM call,
  "Service is DEGRADED: 1 of 2 pod(s) healthy (50%). <pod> is unhealthy (CrashLoopBackOff)."
  with `confidence: 1.0` and `sources: ["k8fy.live-state.prod"]`.
- **Given** a `cert_check` **when** queried **then** list certs and flag those within
  the 30-day window, deterministically.
- **Given** a `general_query` (e.g. "why did payment restart?") **when** queried
  **then** Tier 1 does not handle it; the agent (Tier 2) answers.
- **Given** routing finds pods but the fetch returns no rows **when** queried **then**
  Tier 1 falls through to Tier 2 rather than asserting "healthy" on no data.

## Interfaces

```
# api package (Go)
tryDeterministic(intent string, podData map) -> (QueryResponse, handled bool)
# handled == false  => caller falls through to AgentClient.Reason(...) (Tier 2)

# evaluator package (Go) — canonical impl of spec 002
PodHealth(payload)      -> (status, reason)        # healthy|degraded|unhealthy|unknown
ServiceStatus([]result) -> (status, healthy, ratio)
CertRenewal(payload)    -> (shouldRenew, days, reason)

QueryResponse { answer, status:"ok", confidence:float, sources:[pod_id] }
```

`confidence` is **1.0** for Tier-1 answers — it is a computed rule applied to the
data, not an estimate. (Tier-2/agent confidence remains a model-reported estimate.)
`status` is request-level ("ok"); the health verdict is in `answer` text.

## Known limitations (carried, not hidden)

1. **Entity resolution:** Tier 1 aggregates over all pods in the namespace shard;
   it ignores a service name in the question unless it arrives as an explicit
   `entity` key in the request context. A specifically-named service is better
   served by Tier 2. _Fix later: a name→entities index, or pass the parsed entity
   as the KV lookup key._
2. **Rule duplication:** ~~the health model exists in Go and Python~~ **Resolved
   (2026-06-01):** `reasoning.py` removed; the Go evaluator is the sole implementation
   and spec 002 is the source of truth.
3. ~~**Shallow tier routing:** intent is keyword-based; a "health"-worded causal
   question is mis-routed to Tier 1 (and answered shallowly) rather than Tier 2.~~
   **RESOLVED (2026-06-04, spec 005):** `inferIntent` now matches diagnostic
   phrasing ("why", "what's wrong", "root cause", "investigate", …) as a
   `diagnose` intent *before* health/cert, so causal questions fan out to Tier-2
   correlation instead of the single-signal fast path.

## Open questions

- [ ] Should Tier 1 attempt entity resolution (limitation #1), or is namespace-aggregate
      the intended v1 contract?
- [ ] Add a deterministic `metrics_query` tier (e.g. restart-rate threshold), or keep metrics in Tier 2?
- [x] Where should the rules ultimately live? **Resolved: Go only** (`reasoning.py`
      deleted 2026-06-01). Revisit only if the agent's Tier-2 path needs deterministic
      helpers, in which case prefer a shared definition over a second copy.
- [ ] Should `QueryResponse.status` carry the health verdict (healthy/degraded/unhealthy)
      instead of just "ok", for both tiers?

## Acceptance criteria

- [x] `health_check` with stored pod data returns a verdict with **no LLM/agent call**
      (verified live: ~1ms, 0 Anthropic requests).
- [x] Pod/service verdicts match spec 002 (unit-tested in `evaluator_test.go`).
- [x] Tier-1 answers carry `confidence: 1.0` and cite `sources` (pod IDs).
- [x] Unknown intent or no-data falls through to the agent (Tier 2).
- [ ] `cert_check` Tier-1 path exercised end-to-end against a populated cert pod
      (only health was validated live; cert path is built but unverified).
- [ ] A "why"/free-text question is routed to Tier 2 and answered by the agent.
