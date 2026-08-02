# 0010 – Postgres as the single operational store (MVP)

## Status

Accepted · 2026-06-02 · Amended 2026-08-02 — see note below; the single-store
decision itself stands, but one of its two supporting facts no longer holds.

## Context

**Amendment (2026-08-02):** the "single-tenant" fact below was reversed by
[ADR 0022](0022-multi-tenant-fleet-hub.md) — the store is now multi-tenant
(`tenant_id` + Postgres RLS on the per-customer tables). That does not
reopen *this* ADR's actual decision (still one Postgres instance, no
multi-store split) — it only means "a plain single-tenant schema suffices"
is no longer true; the schema now carries tenant-scoping on top of the same
single store. Kept below unmodified for the historical rationale.

## Context

[ROADMAP P2b](../ROADMAP.md): the design declared six storage engines
(vector/kv/relational/graph/timeseries/logs); the runtime wired three
(Postgres + Redis + Weaviate, the last left nil). Operating multiple stores is
real cost (backups, monitoring, failure modes) and the polyglot stack wouldn't
even run locally. Two facts narrow the choice:

- **Single-tenant per deployment** ([ADR 0009](0009-tenancy-single-tenant-per-deployment.md))
  removes any multi-tenant schema complexity — a plain single-tenant Postgres schema suffices.
- **agentify has no semantic-search feature**, so pgvector + an embeddings pipeline
  would be infrastructure for a capability that doesn't exist (YAGNI).

## Decision

**Postgres is the single operational store.** One instance backs both access
patterns, via two tables sharing one connection pool:

- **current-state** (formerly Redis "kv" — `k8fy.live-state` shards): a
  `current_state` table keyed by `(pod_id, entity_key)`, **upsert latest-wins**,
  with scan (all entities in a pod) and point-lookup (by entity key).
- **append-only** (relational — `k8fy.events`, `k8fy.certificates`): the existing
  `events` table.

The `store_type` taxonomy and `BackendFactory` **stay** — the abstraction is
sound. `kv` and `relational` now both resolve to Postgres-backed stores over one
`*sql.DB`. **Redis is removed** from the runtime and dependencies.

**Deferred (documented, not built):**
- **pgvector / similarity search** — add when a semantic-search feature exists;
  it's a drop-in to the *same* Postgres. The Weaviate client is left inert as the
  documented vector option; pgvector will likely supersede it.
- **S3 + Parquet + DuckDB** cold/analytical history; **ClickHouse/Timescale** for
  high-volume time-series — adopt only when volume justifies it.

## Consequences

- **Positive:** one service instead of three — simpler ops, backups, and local
  dev; single-tenant schema (no `tenant_id`/RLS); JSONB covers semi-structured
  payloads; one place to reason about durability.
- **Negative / cost accepted:** **[Certain] Postgres is slower than Redis for
  high-churn current-state** — revisit at write volume. Local testing now needs a
  real Postgres (mitigated with `embedded-postgres` in tests). No similarity
  search until pgvector is added.
- **Revisit if:** current-state write volume outgrows a single Postgres
  (reintroduce Redis/a cache in front), semantic search lands (add pgvector — same
  DB), or history volume needs columnar/TSDB (DuckDB+Parquet, ClickHouse/Timescale).
