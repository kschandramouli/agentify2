# 0009 – Tenancy: single-tenant per deployment (no in-app multi-tenancy)

## Status

Superseded by [ADR 0022](0022-multi-tenant-fleet-hub.md) · (date: 2026-06-02,
superseded: 2026-08-02) — the go-to-market shifted to a shared multi-tenant
Fleet Hub, exactly the trigger condition named below in "Revisit if." Kept
here, unmodified, as the historical record of why single-tenant was the
right call at the time — read ADR 0022 for the current decision.

## Context

[ROADMAP P3a](../ROADMAP.md) flagged "decide the isolation model now — row-level
vs schema vs DB-per-tenant — because retrofitting tenancy is expensive." That
framing assumes a **shared multi-tenant SaaS**. Two inputs (confirmed 2026-06-02)
say that's not the product:

- **Deployment model: one deployment per customer** — each enterprise runs its own
  agentify instance (own registry, stores, credentials, model provider). This also
  fits the BYO-cloud direction in [ADR 0008](0008-multi-provider-model-routing.md).
- **Scale: single org / internal for now.**

If each customer gets an isolated deployment, the deployment **is** the isolation
boundary. There is no shared blast radius to engineer around, so the shared-SaaS
isolation mechanisms don't apply.

## Decision

1. **agentify is single-tenant per deployment.** Isolation is the deployment
   boundary — its own pod registry, data stores, credentials, and model provider.
   We do **not** build in-app multi-tenancy: no `tenant_id`-scoped row isolation,
   no Postgres row-level security, no per-tenant schemas.
2. **Reserve one cheap seam, don't build enforcement.** Carry a constant
   `DEPLOYMENT_ID` (the customer/org identifier for this instance) in config and
   stamp it on logs/metrics — for fleet observability across deployments and a
   clean future migration path. Stamping it on persisted records is optional and
   deferred (YAGNI for a single tenant). No isolation logic keys off it today.
3. **Rejected for now (analysis preserved):**
   - *Row-level (`tenant_id` + RLS)* — the right choice for a **shared SaaS at
     scale (100s–1000s)**, which we are not.
   - *Schema-per-tenant* — middle ground for a few sensitive tenants on **shared**
     infra; still presumes a shared deployment.
   - *DB-per-tenant* — strong isolation but heavy per-tenant ops; effectively what
     "one deployment per customer" already gives us, without an in-app tenancy layer.

## Consequences

- **Positive:** much simpler. Single-tenant Postgres schema (**unblocks P2b** with
  no multi-tenant complexity); model provider is per-deployment config (**simplifies
  P2c** — no per-tenant-row routing); data governance is per-deployment, not
  per-row; and physical/deployment isolation is the **strongest** posture — the best
  procurement story for sensitive K8s-ops data, and a natural fit for BYO-cloud.
- **Negative / cost accepted:** N deployments to provision, upgrade, and monitor —
  the cost moves to **fleet operations**. This must be paid down with deployment
  automation (Terraform is already in the stack) and cross-deployment observability
  as customer count grows. No single place for cross-customer analytics.
- **Revisit — and supersede this ADR — if:** the go-to-market shifts to a **shared
  multi-tenant SaaS** (many orgs, one deployment) or per-deployment ops becomes the
  scaling bottleneck. Then adopt **row-level** isolation — and do it **before the
  storage schema (P2b) hardens**, because that is the expensive retrofit P3a warned
  about. The rejected options above are the starting menu for that decision.

## Coupled decisions

- **P2b (storage consolidation):** design the Postgres spine as **single-tenant** —
  no `tenant_id` columns / RLS. Simpler and faster.
- **P2c / ADR 0008 (provider routing):** provider/region/credentials are
  **per-deployment config**, not per-tenant-row attributes. The "gated on P3a" note
  in ADR 0008 is now resolved: routing is a deployment-level setting.
- **Data governance (ADR 0007):** "per-tenant classification" is not needed; the
  egress allowlist is a per-deployment policy.
