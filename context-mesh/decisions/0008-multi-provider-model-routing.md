# 0008 – Multi-provider / per-tenant model routing (enterprise)

## Status

Proposed (not built) · 2026-06-02

> Recorded to preserve the analysis. **Deferred until a paying client requires it.**
> Depends on multi-tenancy ([ROADMAP P3a](../ROADMAP.md)) — provider, region, and
> credentials are per-tenant attributes.

## Context

At multi-client enterprise scale, clients impose differing requirements on *where*
model inference runs: data residency/region, compliance regime, preferred cloud,
and how it's billed. Two facts (verified against current docs, 2026-06-02):

- **Claude is not self-hostable.** Earlier wording ("self-hosted") was wrong. The
  only ways to keep inference in a customer's boundary are the provider-operated
  options below.
- **agentify uses only the portable Messages-API surface** — tool use, structured
  outputs, prompt caching, (adaptive) thinking. It uses **no** Managed Agents and
  **no** server-side tools. All of agentify's features are supported on Amazon
  Bedrock, so **Bedrock is viable with zero functional loss.**

### Provider options

| Option | Operated by | Data residency | Feature parity | Auth / billing |
|---|---|---|---|---|
| First-party API | Anthropic | Anthropic infra (ZDR/region on request) | full, earliest | API key / Anthropic |
| Amazon Bedrock | **AWS** | **in customer's AWS account/region** (regional "CRIS" endpoints; +10%) | subset — but all of agentify's needs present | AWS IAM/SigV4 / Marketplace |
| Claude Platform on AWS | **Anthropic** | **data may NOT stay in AWS** (inference can route to Anthropic; `inference_geo` pins geo) | full (Skills, code exec, betas) | AWS IAM/SigV4 / Marketplace |
| Vertex AI / Foundry | Google / Microsoft | in customer's GCP/Azure region | subset (like Bedrock) | GCP/Azure IAM |

Key distinction: **Bedrock/Vertex/Foundry keep data in the customer's cloud
account; Claude Platform on AWS does not** (it's full-feature + AWS billing, but
Anthropic-operated inference).

## Decision (proposed)

1. **Route per-tenant behind a thin provider abstraction** — a client factory keyed
   by tenant config `{provider, region, model_id, credentials}`. The Anthropic SDKs
   expose `Anthropic` / `AnthropicBedrock` / `AnthropicVertex` / `AnthropicAWS` with
   an identical `messages.create(...)` surface, so the agent call site barely changes.
2. **Per-tenant model-ID mapping** — bare (`claude-opus-4-8`) vs Bedrock-prefixed
   (`global.anthropic.claude-opus-4-6-v1`). Bedrock model availability **lags**
   first-party (AWS sets its own schedule); each tenant pins its own model.
3. **Guardrail — stay on the portable Messages-API surface.** Adopting Managed
   Agents or server-side tools would break Bedrock/Vertex/Foundry tenants. Tier-1
   (ADR 0006) is already provider-agnostic (no model), so residency-constrained
   clients get instant deterministic answers regardless of backend.
4. **Provider menu:** residency in customer's cloud → Bedrock/Vertex/Foundry
   (regional); full features + AWS billing, flexible residency → Claude Platform on
   AWS; default/simplest/latest → first-party.
5. **Billing model is a separate business decision:** BYO-cloud (client's own
   Bedrock/Vertex account — they pay, data in their account, compliance theirs) vs
   central (we pay first-party/CPoA and resell). Recommendation: **BYO-cloud** for
   data-sensitive verticals.
6. **Do not build preemptively.** Add a provider only when a paying client requires
   it; building speculatively is wasted effort and an untested integration to carry.

## Consequences

- **Positive:** one client-factory unlocks any provider; portability preserved;
  residency offloaded to the client's cloud (BYO); Tier-1 unaffected by provider.
- **Negative / cost accepted:** portability forecloses first-party-only features
  (Managed Agents, server-side web search) unless we fork per provider; Bedrock
  model lag means those tenants don't get the newest model day one; each provider
  is a testing-matrix + credential-handling + model-ID-drift cost; CPoA does not
  satisfy "data stays in our account."
- **Revisit when:** a client signs requiring residency (build that provider then),
  or agentify wants a first-party-only capability enough to justify a per-provider
  fork. Supersedes the `ANTHROPIC_BASE_URL`-proxy step in [ADR 0007](0007-egress-data-governance.md).
