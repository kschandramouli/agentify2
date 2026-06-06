# Policy: Data Governance (egress)

> **Question this answers:** What data is allowed to leave our boundary to an
> external model, and how is it minimized? See [ADR 0007](../decisions/0007-egress-data-governance.md).

## Principle

**Send the model the least data that lets it answer.** Default to dropping; keep
only what an answer needs. Data leaving the boundary is redacted at one auditable
choke point — the backend, before anything reaches the agent/model.

## Where the gate sits

```
Tier-1 (deterministic) ─────────────────────────────► answer   (no egress)

Tier-2:  store data ──▶ [REDACT] ──▶ agent ──▶ model
         /api/agent/fetch ──▶ [REDACT] ──▶ agent ──▶ model
```

Both backend→agent egress points are redacted (`internal/governance.Redactor`).
The agent never receives un-redacted data, so it cannot forward it to the model.

## The allowlist (config table, not hardcoded policy)

Only allowlisted keys survive; everything else is dropped.

- **Record level:** `entity_key`, `event_namespace`, `type`, `timestamp`, `source`, `payload`.
- **Payload level (K8fy):** `pod_id`, `namespace`, `phase`, `ready`, `restarts`,
  `reason`, `message`, `service`, `endpoints`, `ready_endpoints`, `ready_ratio`,
  `container`, `secret`, `expires_at`, `days_until_expiry`, `should_renew`.

Dropped by default (not allowlisted): annotations, labels, env, raw `conditions`,
and any field a future adapter adds until it is reviewed and added here.

**Rule:** expand the allowlist one field at a time, with review. Never replace it
with "send everything."

## Freeform text (logs) — a weaker, denylist guarantee (ADR 0014)

On-demand pod logs ([spec 008](../specs/008-on-demand-pod-logs.md)) are **freeform
text**, so the allowlist above cannot apply — you can't enumerate the safe "fields"
of a log line in advance. They pass through `Redactor.RedactText`, a **best-effort
denylist** that masks known secret shapes (bearer tokens, AWS keys, JWTs,
`key=secret` pairs, connection-string passwords, emails, long hex/base64 blobs) and
truncates the tail.

⚠️ **This is explicitly weaker than the allowlist.** It will miss novel secret
formats; a log line *can* still carry sensitive data to the model. That weaker
guarantee is the reason logs are **fetched on-demand and never persisted**
([ADR 0014](../decisions/0014-on-demand-ephemeral-log-fetch.md)) — the blast radius
of a miss is one transient prompt, not a permanent store. Do not route logs into
any persistent store under this policy.

## Optional pseudonymization (default off)

When `REDACTION_PSEUDONYMIZE=true`, identifier *values* (`pod_id`, `namespace`,
`service`, `secret`, `entity_key`) become stable hashes (`id_<10hex>`), so the
model can still correlate entities without seeing real names. Off by default
because it degrades operator-facing answers. Known residual: namespace names
embedded in pod-registry IDs are not pseudonymized.

## Egress destination

The model endpoint is overridable via `ANTHROPIC_BASE_URL` for an in-region proxy.
First-class in-region clients (Bedrock/Vertex/Foundry — Claude is not self-hostable)
are future work; see [ADR 0008](../decisions/0008-multi-provider-model-routing.md).

## Non-goals (v1)

- Redacting the operator's free-text **question** (sent to the model verbatim).
- Per-tenant classification (depends on multi-tenancy, ROADMAP P3a).
- Encryption/DLP beyond field minimization.

## How this adapts over time

New integrations register their needed fields into the allowlist (reviewed). The
[refinement-loop](refinement-loop.md) could later flag fields that are sent but
never improve answers, to trim the allowlist.
