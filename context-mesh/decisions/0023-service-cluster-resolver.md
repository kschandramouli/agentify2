# 0023 – Service→cluster resolver + live-fetch auto-routing for diagnosis

## Status

Accepted · (date: 2026-08-03)

## Context

ROADMAP P16 ("multi-cluster connector — wire the existing `Integration`
model into runtime routing") has stood as an unresolved gap since
2026-07-21: nothing in the codebase can answer "which cluster runs this
service?" — `h.adapterClient` (`src/backend/internal/api/handlers.go`) is a
single global built once at startup, never consulting `integrationStore`,
and ROADMAP P18 use case #9's live-fetch (`POST /api/live-fetch`) requires
the caller to already know `cluster_id` explicitly.

Two existing "brain" policies already answer the hard design questions here
— this ADR applies them, it doesn't invent new policy:

- **`policies/storage-strategy.md`**: classify by traits, not by name. A
  service→cluster mapping is `point-lookup + current-state, mutable,
  authority: derived` — the same shape `Integration`/`current_state` already
  are. That routes to a small Postgres table (upsert/full-replace
  semantics), not a new store engine — consistent with ADR 0010's
  single-Postgres decision.
- **`policies/correlation.md`**: when a service resolves to *multiple*
  clusters (the same service name deployed to more than one of a tenant's
  clusters — e.g. staging and prod both running a `payments` namespace), the
  existing fan-out rule already applies: diagnostic intent fans out across
  every matching signal and lets the Tier-2 agent synthesize, surfacing
  disagreement rather than silently picking a winner. This is the same
  Tier-1-vs-Tier-2 split the policy already draws for pods, just widened to
  (pod, cluster) pairs — no new conflict-resolution mechanism needed.

`docs/AGENT_INTEGRATION.md` was consulted as part of this design and found
stale on one point unrelated to this decision: it describes `DiagnoseSkill`
as an Opus-advisor/Sonnet-executor agentic loop, which was replaced by
Pattern A (deterministic pre-fetch + one Claude call) on 2026-06-11 per ADR
0017. This ADR's design follows the current Pattern-A shape.

## Decision

Build the resolver as a small, deterministic registry populated by data the
fleet collector (`agentify-discovery`, ROADMAP P18) already gathers — no new
collection work, just carrying through data that was previously discarded:

1. **`cluster_services` table** (`tenant_id, cluster_id, namespace, service`,
   composite primary key) — same RLS/`tenant_isolation` pattern already used
   for `service_dependencies` (`setTenantContext` + `FORCE ROW LEVEL
   SECURITY`). Populated via a **full delete-then-insert per (tenant,
   cluster) on every collector push** — matches `UpdateIntegrationNamespaces`'s
   "reflects live cluster truth" semantics, not an incremental diff: a
   service that disappeared from the collector's scan disappears from the
   registry on the next push.

2. **Collector-side**: `agentify-discovery`'s inventory scan
   (`main.py`/`inventory.py`, ROADMAP P18 use case #1) already calls
   `k8s_client.list_services(ns)` to decide whether a namespace is "active"
   — it discarded the actual service names after that check. This ADR keeps
   them: `POST /api/cluster-inventory`'s payload grows from
   `{"namespaces": ["a","b"]}` to
   `{"namespaces": [{"name":"a","services":[...]}]}`. Safe breaking change —
   this endpoint has exactly one caller, updated in the same commit.

3. **`GET /api/resolve-cluster?namespace=X&service=Y`** — returns
   `{"cluster_ids": [...]}` (0, 1, or N matches). Same unauthenticated trust
   boundary as `GET /api/service-dependencies` (the agent doesn't present a
   credential today; ADR 0022 Decision #8 already flags agent
   tenant-awareness as a separate, unresolved follow-up) — resolves to
   `DefaultTenantID` via `resolveTenantContext`, same as that endpoint.
   Never errors on "no match" — an empty list is a normal 200, since callers
   are expected to degrade to today's single-cluster behavior, not treat
   "unknown" as a failure.

4. **Agent-side wiring, `DiagnoseSkill` only for this slice**: `_prefetch`
   resolves `service_name`'s cluster(s) and adds one `live_list_pods`
   prefetch task per resolved cluster — routed through the *already-built*
   `process_tool_call` → `_dispatch_live_diagnostic` relay (ROADMAP P18 use
   case #9): passing `cluster_id` in the tool arguments is all that's
   needed, zero new agent-side plumbing. Per the correlation-policy
   reasoning above, **every** resolved cluster gets a task — not just one —
   so Claude sees and can cite each cluster's signal. Other Pattern-A skills
   (`HealthSkill`, etc.) can reuse `resolve_service_clusters` the same way
   later; not wired in this slice.

**Scope boundary — deliberately not solved here:** this resolves
*(namespace, service) → cluster_id(s)* for the agent's own outbound calls.
It does **not** make `h.adapterClient` per-Integration (P16's other
original sub-problem — the ingested `current_state`/`events` read path
staying single-cluster). That remains open; this ADR closes the piece
needed for live on-demand diagnosis specifically, per the immediate ask.

## Consequences

- **Positive:** closes the concrete, actionable half of P16 without waiting
  on the harder `adapterClient` refactor; zero behavior change for every
  deployment that hasn't registered fleet clusters (`cluster_services` is
  empty, resolution returns `[]`, `DiagnoseSkill`'s new step is a no-op);
  reuses `correlation.md`'s existing fan-out policy instead of inventing new
  ambiguity-handling rules.
- **Negative / cost accepted:** `POST /api/cluster-inventory`'s request
  schema is a breaking change (mitigated: single caller, updated together);
  `DiagnoseSkill` diagnosing a service present in N fleet clusters now fires
  N additional live-fetch round trips (each bounded by the existing 15s
  `CollectorHub` timeout) — acceptable given it only engages for tenants
  that have actually onboarded fleet clusters.
- **Revisit if:** the `h.adapterClient` single-global limitation (P16's
  other half) becomes the active blocker for a real multi-cluster ingested-
  data query — that's a separate, larger change (per-Integration client
  cache), not resolved here.
