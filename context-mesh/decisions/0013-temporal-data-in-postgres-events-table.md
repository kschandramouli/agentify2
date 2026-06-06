# 0013 – Temporal data (metric samples & event history) lives in the Postgres events table for v1

## Status

Accepted   ·   (date: 2026-06-05)

## Context

[Spec 005](../specs/005-root-cause-correlation.md) (root-cause correlation) is
bounded to *current-state* health + certs because no temporal data is persisted.
Verified state before this decision:

- The `events` table exists (timestamp-indexed) but **nothing writes to it** in practice.
- The adapter scrapes restart counts every ~30s but emits them to `k8fy.live-state`
  with **current-state traits → latest-wins**, so each sample overwrites the prior
  one. History is destroyed on arrival.
- The trait classifier ([storage-strategy](../policies/storage-strategy.md)) maps
  `time-range-scan` numeric data to a **time-series DB (TSDB)** and append-only logs
  to a **log/search index** — but neither backend is wired (`GetBackend` only knows
  `relational`/`kv`/`vector`), so that classification would error.

So to make diagnosis *causal* ("restarts climbed 0→17 starting 14:10") we need a
temporal store. The forces:

- [ADR 0010](0010-postgres-single-store.md) deliberately consolidated to a single
  Postgres for the MVP to avoid operating multiple datastores.
- We have no Docker / TSDB / managed time-series available locally; a Prometheus/
  Influx/Timescale dependency can't be stood up or validated in this environment.
- MVP volume is low (one cluster, restart samples at a 30s cadence).

## Decision

**Temporal data — metric samples and event history — is stored in the existing
Postgres `events` table for v1.** No dedicated TSDB or log/search index is
introduced yet.

Concretely:
- A new event namespace **`k8fy.metrics`** carries append-only restart-count
  samples (`time-range-scan` / `numeric` / `append-only` traits) → routed to the
  `events` table via the `relational` backend.
- `GetBackend` treats the trait-derived store types **`timeseries`** and **`logs`**
  as aliases of the `relational` (events-table) backend for v1, so trait-based
  classification of temporal data lands in Postgres instead of erroring.
- The events store's `Query` gains time-window (`since`/`until`), entity, type,
  order, and limit parameters so a series can be read over a window.

This **extends** ADR 0010 (single Postgres) to temporal data; it does not
supersede the storage-strategy policy's *target* (TSDB + log index) — that remains
the scale-out design, recorded as "revisit if" below.

## Consequences

- **Positive:** root-cause correlation can become temporal with **zero new infra**;
  fully validatable locally (embedded-postgres); consistent with ADR 0010; one
  backup/restore story.
- **Positive:** "classify, don't enumerate" still holds — temporal data is selected
  by traits; only the *realized* backend is Postgres.
- **Negative / cost accepted:** Postgres is not a TSDB — no native downsampling,
  retention/TTL, or columnar compression. The `events` table will grow unbounded
  without a retention job (deferred). Wide time-range scans rely on the
  `idx_events_timestamp` index and will degrade at high cardinality/volume.
- **Negative:** restart counts are cumulative gauges, not rated metrics; "rate"
  questions require a deterministic delta over samples (kept out of the LLM).
- **Revisit if:** sample volume or query latency outgrows a single Postgres
  (introduce table partitioning by time, then a real TSDB for metrics and a
  log/search index for events — the storage-strategy target); or retention/PII
  rules require per-namespace TTL the events table can't cheaply express.
