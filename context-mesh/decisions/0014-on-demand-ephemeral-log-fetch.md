# 0014 – On-demand ephemeral log fetch (backend ↔ adapter), best-effort text scrubbing

## Status

Accepted   ·   (date: 2026-06-05)

## Context

Diagnosis needs the **crash reason** ("OOMKilled", a panic stack trace, "connection
refused to db") to move from *when* a problem started to *why*. That reason lives
in **pod logs**. But logs are unlike the structured signals we ingest:

- **Volume:** a single crashing pod emits thousands of lines — orders of magnitude
  more than metric samples. The premise of [ADR 0013](0013-temporal-data-in-postgres-events-table.md)
  (low volume → Postgres) does not hold for logs.
- **Policy:** [storage-strategy](../policies/storage-strategy.md) routes logs to a
  **log/search index**, explicitly *not* relational. Persisting raw logs in the
  events table would knowingly violate that.
- **Governance:** logs are the #1 leak path for secrets/PII (tokens, connection
  strings, user data). The egress redactor (ADR 0007) is an **allowlist over
  structured payloads** — it cannot be applied to freeform text, where you cannot
  enumerate the safe fields in advance.

Two architectural facts constrain the design: the **backend** holds no Kubernetes
client (only the **adapter** does), and ADR 0007 places the egress boundary at
**backend → agent**.

## Decision

**Logs are fetched on demand, kept ephemeral, and never stored.**

1. **Live fetch path (new):** the agent's `get_pod_logs` tool → backend → a new
   internal **adapter HTTP endpoint** (`POST /logs`) that calls the K8s
   `read_namespaced_pod_log` API for a **bounded tail** (default 100 lines, hard cap
   200) of the current or `previous` (crashed) container. This is the first
   backend→adapter synchronous call; the backend otherwise only reads storage.
2. **Never persisted:** log text is returned through the request and dropped. It
   never enters Postgres, a pod, or the events table. No retention question arises.
3. **Best-effort text scrubbing at egress:** the backend applies a **pattern-based
   scrubber** (`RedactText`) — bearer tokens, AWS keys, JWTs, `key=secret` pairs,
   connection-string passwords, emails, long hex/base64 blobs — and caps total size
   before the agent sees the text. This runs at the same egress boundary as the
   allowlist (ADR 0007).

## Consequences

- **Positive:** delivers the crash "why" with **no log store**, no retention/volume
  liability, and a bounded token cost; respects the storage-strategy policy (no
  raw logs in relational); keeps the K8s client solely in the adapter.
- **Negative / cost accepted — weaker guarantee, stated plainly:** `RedactText` is a
  **denylist**, not an allowlist. It will miss novel secret shapes; logs *can* still
  leak sensitive data to the model. This weaker guarantee is the explicit reason
  logs are **ephemeral and never persisted** — a transient leak to the model is the
  bounded blast radius, versus a permanent one in storage.
- **Negative:** introduces a backend→adapter dependency and an HTTP surface on the
  adapter (previously a one-way emitter). It uses the Python stdlib HTTP server (no
  new dependency) for a single internal route. That surface is guarded by a shared
  **bearer token** (`ADAPTER_AUTH_TOKEN`, constant-time compare; `/health` stays
  open). The token is optional for local dev (a warning is logged) but must be set
  in prod — otherwise anything that can reach the port can pull pre-redaction logs.
- **Negative:** no local validation against a real cluster (no Docker/k8s here); the
  egress-scrub path is validated via a stubbed adapter (httptest), not end-to-end.
- **Revisit if:** log access becomes high-frequency (cache or stream), a real log
  index is introduced (then search replaces tail-fetch), or governance requires a
  stronger guarantee than pattern scrubbing (e.g. structured-only log contracts, or
  scrubbing in the adapter before the text ever leaves the cluster).
