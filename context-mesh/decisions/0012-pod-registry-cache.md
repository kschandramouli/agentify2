# 0012 – Pod-registry read-through cache (resilience + Scan elimination)

## Status

Accepted · 2026-06-03

## Context

The pod registry is read on the hot path of every query and write path of every
ingest. Two problems:

- **Performance/cost:** `ListPods` is a full DynamoDB **Scan**, run per query
  (fan-out routing, general queries, cert lookups, `/admin/pods`). O(table) on the
  hot path — worsens with every pod.
- **Availability (the SPOF the review flagged):** if DynamoDB is briefly
  unavailable/slow, `RouteToPods` errors and **every query 500s**.

The pod **set** is low-churn (changes only on pod formation, ADR 0005) and small
(single-tenant per deployment, ADR 0009), yet it's read constantly — the textbook
case for an in-process read-through cache. Crucially, this caches pod **metadata**
(which pods exist + store_type/shard map), **not** live health data — answers stay
fresh because current-state is read from Postgres per query (ADR 0010).

## Decision

Wrap the DynamoDB-backed registry in an in-process **snapshot cache**
(`registry.Cache`, behind a `registry.PodStore` interface):

- **Whole-set snapshot.** One `ListPods(nil)` loads the entire (small) pod set;
  all reads — `GetPod`, `ListPods(filter)`, `ListPodsByNamespace`, `ListActivePods`
  — are served from it. Replaces per-query `Scan`/`GetItem` with a periodic load.
- **TTL refresh** (default 30s): a read past the TTL refreshes the snapshot.
- **Serve-stale on error:** if a refresh fails, serve the last good snapshot up to
  a max-stale bound (default 5m), logging + counting it — so a DynamoDB blip
  degrades to *slightly stale routing*, not a 500. Past max-stale, reads error.
- **Invalidate only on set-changing writes:** `UpsertPod`/`DeletePod` invalidate
  the snapshot (a newly-formed pod/shard is visible on the next query).
  `UpdateFreshness`/`UpdateQueryStats` (frequent, per-ingest, not routing-critical)
  pass through **without** invalidating — so heavy ingest doesn't cold the cache.
- **DynamoDB only.** The in-memory registry mode (local dev, ADR 0009) is not
  wrapped — it's already in-process with no Scan or SPOF.
- **Observed** via `agentify_registry_cache_total{result=hit|miss|stale|error}`.

## Consequences

- **Positive:** per-query registry Scans gone (latency + DynamoDB cost); transient
  registry unavailability degrades gracefully (stale routing) instead of failing;
  newly-formed pods still appear promptly (invalidate-on-Upsert); the cache is
  unit-testable via a fake `PodStore` (no DynamoDB needed).
- **Negative / cost accepted:** routing can be **up to TTL stale** for pod *metadata*
  (not data) — acceptable since the set changes rarely and Upserts invalidate; query
  *stats/freshness* in `GetStats` lag up to TTL; one more abstraction (the interface).
  Serving stale during an outage means routing on a possibly-outdated pod set —
  bounded by max-stale and logged.
- **Revisit if:** the pod set grows large enough that a full-set snapshot is heavy
  (move to per-key caching + a real `Query` instead of `Scan`), or true registry HA
  is needed beyond DynamoDB's managed multi-AZ (then a replica/fallback store).
