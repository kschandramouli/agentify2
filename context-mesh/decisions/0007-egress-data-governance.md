# 0007 – Egress data governance: allowlist redaction at the agent boundary

## Status

Accepted   ·   2026-06-01

## Context

The Tier-2 path sends fetched pod data to an external model API (Anthropic). The
data can carry sensitive material — namespaces, pod/service/secret names, and
(as payloads grow) annotations, env, labels. Shipping it raw is a procurement-
killing finding for any enterprise security review, and there is currently **no
redaction and no control over where the data egresses**. See
[ROADMAP §P2a](../ROADMAP.md).

Tier-1 (deterministic, ADR 0006) never egresses, so this concerns the Tier-2
agent path only — but that path has two egress points: the `/reason` request the
backend sends, and the data returned from `/api/agent/fetch` during the agent's
tool loop.

## Decision

1. **Allowlist, not denylist.** Redaction keeps only the fields the reasoning
   needs and drops everything else. A denylist of "sensitive-looking" keys leaks
   every field we failed to anticipate; an allowlist fails safe as payloads grow.
2. **Redact at the backend→agent boundary.** The backend owns the data and the
   governance policy; the agent is a conduit to the model. Both egress points
   (`/reason` input, `/api/agent/fetch` output) are redacted in the backend, so
   whatever the agent sends to the model is already minimized. Implemented in
   `internal/governance` (allowlists in code as a config table) and applied in the
   `/api/query` and `/api/agent/fetch` handlers.
3. **Pluggable egress destination — config for v1.** The model endpoint is
   overridable via `ANTHROPIC_BASE_URL` (honored by the SDK), enabling an
   in-region proxy. First-class in-region clients (Bedrock/Vertex/Foundry — Claude
   is not self-hostable) are a follow-up, not built here. See [ADR 0008](0008-multi-provider-model-routing.md).
4. **Pseudonymization is opt-in, default off.** When enabled, identifier *values*
   (pod/service/secret names, entity keys) are replaced with stable hashes.
   Default off because it degrades operator-facing answers (`id_3f9a…` vs
   `payment-svc`); turn on when a customer review requires it.

## Consequences

- **Positive:** removes the raw-egress finding; minimizes tokens sent to the
  model; gives one auditable choke point; in-region routing is a config flag.
- **Negative / cost accepted:** an allowlist can **starve the open-ended Tier-2
  agent** of context as questions broaden — the allowlist must be expanded
  *deliberately* and never widened to "send everything." The operator's free-text
  **question is not redacted** (v1 non-goal) — a determined operator can still put
  a secret in the prompt. Pseudonymization does not cover namespace names embedded
  in pod-registry IDs (a known residual when enabled).
- **Revisit if:** Tier-2 needs richer context (expand the allowlist per field,
  with review), or when in-region model clients are built (Bedrock/Vertex/Foundry —
  [ADR 0008](0008-multi-provider-model-routing.md) — supersedes the
  `ANTHROPIC_BASE_URL`-only step), or multi-tenancy (ROADMAP P3a) requires
  per-tenant classification.

See [policies/data-governance.md](../policies/data-governance.md) for the living rules.
