# 0016 – Proactive investigation & notification loop (push, human-in-the-loop)

## Status

Accepted   ·   (date: 2026-06-06)

## Context

Everything so far is **pull**: a human asks a question → the system diagnoses. P4c
adds the inverse — the system notices a problem on its own and **pushes** a
diagnosis to humans. This is the first feature that (a) runs the LLM **unattended**,
(b) sends data to a **third-party destination** (Slack/webhook), and (c) acts
without a person in the request path. Each is a new risk class:

- **Alert noise / flapping** — the classic failure of any alerting system. Firing on
  every bad event spams humans and re-burns LLM calls.
- **Unbounded LLM spend** — every investigation is an Opus call; an event storm
  could fan out into many.
- **Egress to a new destination** — a summary leaving toward Slack must be governed
  (ADR 0007), and it must never cross the read-only boundary (ADR 0003).

We have no ML (P4b-ML deferred), but we *do* have the deterministic Tier-1
evaluator (`PodHealth` / `ServiceStatus` / `CertRenewal`) — enough to trigger
honestly without claiming anomaly detection.

## Decision

A **periodic, deterministic, human-in-the-loop investigation loop**:

1. **Trigger — deterministic sweep.** An in-process janitor (like ADR 0015) ticks
   every `INVESTIGATION_SWEEP_INTERVAL_MINUTES`, scans `current_state`, and applies
   the evaluator. A **namespace** is anomalous if any pod is `UNHEALTHY` or any cert
   in it is within `INVESTIGATION_CERT_CRITICAL_DAYS`. No ML; no statistical anomaly
   detection — and we say so.
2. **Incident key = namespace.** One open incident per namespace (live-state is
   sharded by namespace; pod payloads carry no service label). This is the dedup
   unit. Investigate a namespace only on the **transition into** anomalous; notify
   again on **resolve**. No repeat alerts while it stays bad.
3. **Bounded spend.** At most `INVESTIGATION_MAX_PER_SWEEP` new investigations per
   tick; a per-namespace **cooldown** suppresses re-open churn. Each investigation
   reuses the existing `diagnose` path (one Opus call).
4. **Governed egress, read-only.** The outbound summary is built from
   already-allowlist-redacted data (ADR 0007) and passed through the log text
   scrubber as defense-in-depth; it **describes and recommends only** — never acts
   (ADR 0003). Destination is a generic Slack-compatible webhook.
5. **Opt-in.** `INVESTIGATION_ENABLED` defaults to **false**. Proactive spend +
   outbound alerts must be deliberately turned on (and a webhook URL set).

## Consequences

- **Positive:** turns the diagnosis capability into proactive ops value; no new
  infra (in-process ticker, one webhook POST); honest deterministic trigger; reuses
  diagnose + redaction wholesale.
- **Negative / cost accepted — deterministic triggers are coarse:** UNHEALTHY/cert
  thresholds catch hard failures, not subtle regressions (latency, slow leaks) that
  need the deferred ML. False negatives are expected; this is a safety net, not full
  monitoring.
- **Negative — flapping within a sweep window is invisible:** a namespace that
  breaks and recovers between ticks is never seen. Acceptable for a 5-min cadence;
  tighten the interval if it matters (at LLM-cost).
- **Negative — single in-process loop:** multiple backend replicas would each sweep
  and could double-alert (incident state is in-memory, per-process). Fine at MVP;
  revisit with shared incident state / leader election at scale.
- **Negative — best-effort egress scrub on prose:** the summary is model-generated
  text; the scrubber only catches secret-shaped tokens. Bounded because the agent
  only ever saw allowlist-redacted input.
- **Revisit if:** we need statistical anomaly triggers (P4b-ML), provider-specific
  paging (PagerDuty), shared incident state across replicas, or richer alert
  formatting (Slack Block Kit).
