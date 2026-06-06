# Policy: Refinement Loop

> **Question this answers:** How does real-time feedback (streamed events + query
> outcomes) continuously reshape *what is stored where* and *how it's routed*?
>
> This is the policy that makes the mesh **self-improving** rather than static.
> One of the four "brain" policies.

## Principle

_<one sentence, e.g. "Continuously align storage layout and routing with observed
query patterns; the cheapest correct answer should get faster over time.">_

## The loop

```
observe → measure → decide → act → observe …
```

1. **Observe** — what signals do we collect?
   - [ ] Query hits / misses per pod
   - [ ] Latency (p50/p95) per pod
   - [ ] "No good answer" events
   - [ ] Correlation queries (which pods get queried together)
   - [ ] Incoming event volume / drift per topic
   - _<others?>_

2. **Measure** — what do we compute from those signals?
   - _<e.g. miss-rate per pod, store-type mismatch score, co-query affinity matrix>_

3. **Decide** — what thresholds trigger an action?
   - _<e.g. "if pod miss-rate > 20% over 1h → re-evaluate its storage strategy"
     or "if two pods co-queried > 50% of the time → propose merge">_

4. **Act** — what changes can the loop make?
   - [ ] Re-route (update orchestrator scoring)
   - [ ] Re-store (migrate a pod to a better store type — see [storage-strategy](storage-strategy.md))
   - [ ] Re-shape pods (split/merge/retire — see [pod-formation](pod-formation.md))
   - [ ] Re-summarize / compress cold data

## Cadence

- **Real-time / streaming reactions:** _<which decisions happen per-event?>_
- **Batch / periodic reactions:** _<which happen on a schedule? how often?>_

## Safety rails

- _<how do we avoid thrashing, e.g. a pod that splits then immediately merges?
  Hysteresis? cooldown windows? change budget per hour?>_
- _<how do we roll back a bad refinement decision?>_

## How we know it's working

- _<target metrics: miss-rate down, p95 latency down, storage cost flat or down>_

## Open questions

- [ ] Is the decision logic rules-based, ML-based, or LLM-driven (Claude evaluating stats)?
- [ ] Do refinements require human approval initially?
- [ ] How is feedback persisted and made queryable for the loop itself?
