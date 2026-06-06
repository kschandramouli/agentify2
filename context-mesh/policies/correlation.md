# Policy: Correlation Across Pods

> **Question this answers:** When a single pod can't answer a query, how does the
> orchestrator fan out to multiple pods, combine their results, and resolve
> conflicts into one coherent answer?
>
> One of the four "brain" policies.

## Principle

Prefer the smallest set of pods that fully answers the query. For **diagnostic**
questions, fan out across **all of a service's signals** and let the Tier-2 agent
synthesize a prioritized causal narrative — citing each signal and surfacing
uncertainty rather than inventing a cause. (Realized in [spec 005](../specs/005-root-cause-correlation.md).)

## When to correlate (vs. single-pod)

- [x] **Diagnostic intent** — "why…", "what's wrong with…", "root cause", "investigate".
  `inferIntent` maps these to `diagnose` *before* health/cert, so they don't fall
  into the single-signal Tier-1 path.
- [x] Query spans multiple signal families owned by different pods (health + certs + events).
- [ ] Single-signal confidence below threshold → escalate to a fan-out (future).

A single, clear single-signal question (e.g. "is X healthy?") stays Tier-1.

## Fan-out strategy

- **Which pods?** For `diagnose`, all `k8fy` leaf pods for the namespace (the
  service's live-state shard + certificates + events if present). Index pods are skipped.
- **Parallel or sequential?** The backend fetches each pod's data up front (parallel
  in effect), hands the combined set to the agent, which may then call tools for more.
- **Budget:** the agent's tool-loop iteration cap (config) bounds depth; the egress
  allowlist (ADR 0007) bounds the data/tokens sent to the model.

## Combining results

- **Merge method:** **the Tier-2 agent synthesizes** — this is not a deterministic
  merge. It produces a per-signal finding, a likely cause, a severity, and a
  prioritized action list. (Deterministic single-signal answers stay in Tier-1.)
- **Deduplication:** handled by the model over a small signal set; not a separate pass.

## Conflict resolution

- **Authority order:** the K8s API is system-of-record for live-state; certs come
  from the cert scrape. Signals are usually orthogonal (different facts), so true
  conflict is rare in v1.
- **Surface vs. resolve:** **surface** — the agent states disagreement/uncertainty
  rather than silently picking a winner.

## Output contract

A correlated answer is a narrative naming the **active incident**, **latent
risk(s)**, a **likely cause**, and a **prioritized** action order, plus structured
`findings` / `likely_cause` / `severity` / `recommendations`. It cites the
contributing pods (`sources`) and carries a `trace_id` ([spec 004](../specs/004-query-provenance.md))
so the diagnosis is auditable.

## How this policy adapts over time

- _<learn co-query affinities → pre-join hot pod pairs? cross-ref
  [pod-formation](pod-formation.md) merge rules.>_

## Open questions

- [ ] Do we cache correlated results, and how do we invalidate them on new events?
- [ ] How do we handle one pod being slow/unavailable during fan-out?
- [ ] Is there a max "hops" for chained correlation before we give up?
