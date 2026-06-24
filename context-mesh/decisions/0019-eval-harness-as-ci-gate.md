# 0019 – Eval Harness as CI Gate

## Status

Accepted   ·   (date: 2026-06-17)

## Context

The eval harness has been listed as P5+ ("later — explicit deferral") since the
project began. A technical review identified this as the most damaging gap relative
to evaluation criteria: the feedback explicitly praised evaluation pipeline work, but
in agentify it exists only in the roadmap, not in code.

Two compounding problems:

1. **Silent regressions are now live.** We updated `k8fy/health-check` and
   `k8fy/diagnose` prompts in Langfuse (June 2026) with new output schemas
   (`HEALTH_REASONING_SCHEMA`, `DIAGNOSE_REASONING_SCHEMA`). There is no automated
   check that the new prompts produce valid structured output or that diagnostic
   quality has not regressed. Every prompt change is a blind deploy.

2. **The infrastructure is already in place.** Langfuse is wired; `trace_id` is
   returned on every query; `query.trace` logs are structured. The missing piece is
   the test dataset and the CI step that runs against it.

The deferral rationale was "skills must be stable before evals are meaningful." The
skills are now stable (all five on Pattern A, ADR 0017). The deferral is no longer
justified.

## Decision

**Promote the eval harness from P5+ to immediate next action (P7).** Build it now,
not after the next feature.

### Architecture

```
Langfuse dataset: "k8fy-regression"
  │
  ├── Each item: { input: QueryRequest, expected: GroundTruth }
  │
  └── GroundTruth: {
        intent: "health_check" | "diagnose" | "cert_check" | ...,
        tier: "tier1" | "tier2",
        status: "healthy" | "degraded" | "unhealthy",
        min_findings_count: int,  // for Tier-2 skills
        must_contain_field: ["likely_cause", "headline"],  // non-null check
        latency_ms_p95: int  // regression guard
      }
```

**Eval run (CI-triggered, post-deploy):**
```
scripts/run_evals.py
  for each dataset item:
    response = POST /api/query (intent, namespace, service)
    score = evaluate(response, ground_truth):
      - correct_intent? (exact match)
      - correct_tier? (exact match)
      - correct_status? (exact or within-band for health)
      - findings_present? (count >= min)
      - no_null_required_fields? (headline, likely_cause present on Tier-2)
      - latency_within_budget? (p95 < threshold)
    langfuse.score(trace_id, score)

  if mean_score < PASS_THRESHOLD:
    exit(1)  # blocks deploy
```

**Dataset design — minimum viable (10 cases):**

| # | Query | Expected intent | Expected tier | Expected status |
|---|-------|----------------|--------------|----------------|
| 1 | "is payment-worker healthy?" (all pods OK) | health_check | tier1 | healthy |
| 2 | "is payment-worker healthy?" (pods crashing) | health_check | tier2 | unhealthy |
| 3 | "why is payment-worker crashing?" | diagnose | tier2 | unhealthy |
| 4 | "check TLS certs for payment-api" | cert_check | tier1 | ok / warn |
| 5 | "show restart trend for payment-worker" | metrics_history | tier2 | — |
| 6 | "what changed in payments recently?" | change_history | tier2 | — |
| 7 | "diagnose staging/payment-worker" | diagnose | tier2 | (any) |
| 8 | Tier-1 health check returns in < 100ms | health_check | tier1 | healthy |
| 9 | Tier-2 diagnosis includes likely_cause or null with reason | diagnose | tier2 | — |
| 10 | Unknown query routes to general_query | general_query | tier2 | — |

**CI integration:**
```yaml
# .github/workflows/deploy.yml (new step, post-rollout)
- name: Run eval regression suite
  run: |
    python scripts/run_evals.py \
      --backend-url http://localhost:18080 \
      --langfuse-project k8fy \
      --dataset k8fy-regression \
      --pass-threshold 0.85
```

### Langfuse integration points

- `trace_id` is already returned on every `/api/query` response
- `langfuse.score(trace_id, name, value)` attaches the eval score to the existing trace
- Eval results visible in Langfuse UI alongside the production traces

## Consequences

- **Positive:** Prompt changes are no longer blind deploys. Any regression in
  intent classification, tier routing, or structured output fields is caught in CI.
- **Positive:** Closes the "evaluation pipeline" credibility gap. The artifact
  (Langfuse dataset + CI eval step) is now demonstrable code, not a roadmap item.
- **Positive:** Forces explicit ground truth definitions — the act of writing the
  dataset surfaces assumptions about what "correct" means for each intent class.
- **Negative / cost accepted:** Each eval run makes ~10 real API calls to the
  deployed backend (Tier-2 calls hit Claude). Cost: ~$0.50/run at current rates.
  Acceptable for post-deploy gates; not acceptable for per-commit CI. Gate: run only
  after rollout completes, not on every PR.
- **Revisit if:** Dataset grows beyond 50 cases — at that point consider the Batch
  API (50% discount) for eval runs to control cost.
