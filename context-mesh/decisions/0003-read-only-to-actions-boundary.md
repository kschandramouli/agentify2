# 0003 – Read-only → actions boundary: when K8fy moves from observe to act

## Status

Proposed   ·   2026-05-30

## Context

K8fy currently answers questions about Kubernetes health and certificate status
(read-only). The health model itself implies future *actions* — "should we renew
this cert?" is only useful if the system can *actually renew it*.

But acting on production infrastructure is risky. A bad renewal can break TLS.
A bad restart cascades. We need clear guardrails on when K8fy can transition
from **observe** (answer questions) to **act** (take actions).

## Decision

K8fy transitions to read-write in **phases**, starting with the safest action:
**automatic certificate renewal**. The boundary is:

### Phase 1 (current): Read-only + observability

- K8fy **answers** "should we renew?" based on the [health model](../specs/002-k8fy-health-queries.md).
- It logs/emits what *would* happen (dry-run mode): *"Cert for service X expires in 12 days; renewal policy triggers."*
- **No actual actions taken.** Humans review and trigger renewal manually if desired.
- **Duration:** at least 2 weeks observing real queries and misses.

### Phase 2 (future): Auto-renew with guardrails

Once we're confident the health model is correct:
- **Cert renewal becomes automatic** when:
  - Expiry date hits the threshold (30 days)
  - Pod/service has been in Healthy state for ≥ 5 days (grace period)
  - At least one prior successful renewal has been observed (proof it works)
- **All renewals are:**
  - Logged with cert details, old/new expiry, who triggered it
  - Reversible (keep the old cert available for rollback for 7 days)
  - Accompanied by a **Prometheus counter** (so on-call can alert if renewal rate goes haywire)
- **Rollback:** if cert renewal causes outages, one-line command disables auto-renewal for that service.

### Phase 3 (much later): Other actions?

- Restarting pods (high risk; requires stricter gates)
- Auto-scaling (needs different signals)
- ...not in scope yet.

## Consequences

- **Positive:** removes manual cert renewal work; aligns infrastructure with policy.
- **Negative / cost accepted:** adds operational complexity; a bad renewal can break TLS and wake on-call; requires monitoring/alerting; rollback coordination needed.
- **Revisit if:** the health model turns out to be noisy (false positives for "should renew"), or cert renewals never fail so guardrails feel like overhead.
