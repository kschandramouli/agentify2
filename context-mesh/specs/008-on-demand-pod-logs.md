# 008 – On-demand pod logs (the crash "why", ephemeral)

> Lets the agent pull a **bounded, redacted tail** of a crashing pod's logs at
> diagnosis time — without storing logs. Realizes [ADR 0014](../decisions/0014-on-demand-ephemeral-log-fetch.md).

> **Superseded (2026-08-04) — the adapter-log-server half of this spec.**
> [ADR 0027](../decisions/0027-merge-k8fy-adapter-into-discovery.md) retired
> the standalone adapter's inbound `POST /logs` HTTP server and the `get_pod_logs`
> tool/`AdapterClient.FetchLogs` path described below outright — not
> ported, not deprecated-in-place. The capability lives on through
> `live_get_pod_logs`, relayed over agentify-discovery's existing
> persistent outbound connection (no inbound port, no separate
> `ADAPTER_AUTH_TOKEN`); `get_logs`'s router already prefers that path.
> The redaction/scope/behavior sections below (bounded tail, `RedactText`
> scrub, never persisted, `previous=true` for crash logs) still describe
> today's behavior faithfully — only the transport and interface changed.
> Left in place rather than rewritten so the original interface this
> spec shipped against stays legible.

## Goal

When events/metrics show *that* and *when* a pod crashed but not *why*, fetch the
last lines of the (previous, crashed) container to find the reason — "OOMKilled", a
panic/stack trace, "connection refused", a failing readiness probe — and feed that
into the diagnosis.

## Depends on

- [ADR 0014](../decisions/0014-on-demand-ephemeral-log-fetch.md) — live ephemeral
  fetch path + best-effort text scrubbing.
- [ADR 0007](../decisions/0007-egress-data-governance.md) — egress boundary; this
  adds a *text* scrubber alongside the structured allowlist.

## Scope (v1)

**In:**
- Adapter exposes `POST /logs` (stdlib HTTP, no new dep) → `read_namespaced_pod_log`
  for a **bounded tail** (default 100, cap 200 lines) of `container`, optionally the
  `previous` (crashed) instance.
- Backend `get_pod_logs` tool path: backend → adapter → **`RedactText` scrub** →
  agent. Logs are **never persisted** and never touch storage.
- Diagnose prompting: when a crash lacks a clear reason, fetch `previous=true` logs
  and quote the relevant failure line.

**Out (stay honest):**
- Log storage, search, or history — none. Each fetch is live and discarded.
- Strong redaction guarantee — `RedactText` is **best-effort pattern scrubbing**
  (denylist), not the allowlist guarantee. Logs *may* still leak; that is the
  accepted, bounded cost of an ephemeral fetch (ADR 0014).
- Multi-container fan-out, label selectors, streaming/follow.

## Behavior

- **Given** `payment-7c9-bbb` is CrashLoopBackOff and pod-events returned nothing
  **when** diagnosing **then** the agent calls `get_pod_logs(previous=true)`, reads
  e.g. "OOMKilled" / "panic: nil map", and names that as the crash reason.
- **Given** the log line contains `password=hunter2` or a bearer token **then** the
  scrubber masks it before the agent sees the text.
- **Given** the pod/log is unavailable **then** the tool returns an error string the
  agent reasons about; it does not crash the loop.

## Interfaces

```
adapter POST /logs  { pod_id, namespace, container?, previous?, tail_lines? }
   Authorization: Bearer <ADAPTER_AUTH_TOKEN>   # required when configured (/health open)
                 →  { pod_id, container, previous, logs }   # raw, internal hop only

agent tool get_pod_logs(pod_id, namespace, container?, previous?, tail_lines?)
backend: forwards to adapter (with bearer token) → RedactText(logs) → returns to agent (not stored)
```

## Acceptance criteria

- [ ] `get_pod_logs` returns a bounded tail; never writes to any store.
- [ ] Secrets matching the scrub patterns (tokens, keys, `pwd=`, conn-string
      passwords, emails, long blobs) are masked at the egress boundary.
- [ ] Tail length is capped regardless of the requested value.
- [ ] An unavailable pod yields an error payload, not a crash.
- [ ] When `ADAPTER_AUTH_TOKEN` is set, `/logs` rejects requests without the
      matching bearer token (constant-time); the backend sends it on every fetch.
