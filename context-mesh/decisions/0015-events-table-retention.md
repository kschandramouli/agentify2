# 0015 – Scheduled retention for the events table

## Status

Accepted   ·   (date: 2026-06-05)

## Context

The Postgres `events` table is now append-only history for three signals: metric
samples ([spec 006](../specs/006-temporal-ingestion-and-history.md)), deploy/change
events ([spec 007](../specs/007-change-events.md)), and cert checks. [ADR 0013](0013-temporal-data-in-postgres-events-table.md)
explicitly deferred retention, flagging unbounded growth as the cost to revisit.

The dominant volume is **metric samples**: one row per container per scrape
(~every 30s). Left unbounded, the table grows without limit, the
`idx_events_timestamp` index degrades, and storage costs climb. The traits already
carry a `retention` hint (30d), but it is not stored per row.

Constraints: no scheduler/cron infrastructure is provisioned (ECS Fargate, no
EventBridge wired); we run a single backend process; the only datastore is Postgres
(ADR 0010).

## Decision

Run an **in-process retention janitor** in the backend: a background goroutine on a
ticker deletes `events` rows whose **event `timestamp`** is older than a configurable
window.

- `EVENTS_RETENTION_DAYS` (default **30**) — the age window. `0` disables the janitor.
- `EVENTS_RETENTION_INTERVAL_MINUTES` (default **60**) — how often it runs.
- Deletes from `events` only. `current_state` is latest-wins (bounded by entity
  cardinality) and is **never** purged.
- Each run logs the rows deleted and increments a metric (ADR 0011).

A single global window for the whole table (not per-namespace/per-signal) for v1.

## Consequences

- **Positive:** bounds table age with **no new infra**; runs and is testable locally
  (embedded-postgres); honours ADR 0013's "revisit if retention".
- **Negative / cost accepted — bounds age, not rate:** between purges the table
  still grows at the sample ingestion rate; a 30s metric cadence is a lot of rows
  within a 30-day window. Retention caps the tail, it does **not** curb volume. The
  real fix is **downsampling/rollups** for metrics (deferred) — until then, tune
  `EVENTS_RETENTION_DAYS` down if storage pressure appears.
- **Negative:** deletion is destructive and runs unattended — a misconfigured tiny
  window silently discards history. Mitigated by an explicit default and a `0`
  disable switch, and by logging counts each run.
- **Negative:** a single in-process janitor means if multiple backend replicas run,
  each ticks independently (idempotent `DELETE`, so correct, just redundant). Fine
  at MVP scale; revisit with leader-election or a real scheduler if it matters.
- **Revisit if:** metrics move to a TSDB with native retention (then this janitor
  covers only deploy/cert events), or volume forces downsampling, or multi-replica
  redundancy becomes wasteful.
