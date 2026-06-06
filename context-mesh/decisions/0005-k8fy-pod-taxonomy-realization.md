# 0005 – K8fy pod taxonomy: how ingestion and routing realize the policy pods

## Status

Accepted   ·   2026-06-01

## Context

The ingester and the query router had drifted apart and neither matched the
policies. Ingestion created one pod per `event_namespace.event_type`
(e.g. `k8fy.live-state.pod_modified`) with `pod.Namespace` set to the event
namespace, while routing looked pods up by `Namespace == "k8fy"` and by store
type. As a result, ingested data and queries never met: data landed in pods the
router never inspected.

The policies already specify the intended shape:

- [storage-strategy](../policies/storage-strategy.md) — events are classified by
  an **event profile** keyed on `event_namespace` ("classify, don't enumerate"),
  which yields a store family per family of events.
- [pod-formation](../policies/pod-formation.md) + [ADR 0002](0002-pods-are-recursive.md)
  — `k8fy.live-state` is an **index pod** sharded **by namespace**; its child
  **leaf shards** (one per K8s namespace) hold the data.

The seed fixtures (`tests/fixtures`) already encode this taxonomy, so they served
as the concrete contract to align both sides to.

A second, narrower force: only the **KV (Redis)** and **relational (Postgres)**
backends are wired today. The policy-preferred log/search index and TSDB don't
exist yet.

## Decision

Drive pod formation from an **event-profile registry**
([`internal/config/pod_profiles.go`](../../src/backend/internal/config/pod_profiles.go)),
keyed on `event_namespace`, and realize the K8fy pods exactly as the policy +
fixtures specify:

| Pod | Kind | `store_type` | Sharding |
|-----|------|--------------|----------|
| `k8fy.live-state` | index | `passthrough` (holds shard map only) | parent of the shards below |
| `k8fy.live-state.<ns>` | leaf | `kv` | one shard per K8s namespace (`payload.namespace`) |
| `k8fy.certificates` | leaf | `relational` | single pod |
| `k8fy.events` | leaf | `relational` | single pod |

Concretely:

- **`pod.Namespace` = the integration** (`"k8fy"`). The **K8s namespace is the
  partition**, encoded in the shard pod ID (`k8fy.live-state.prod`) and in the
  index pod's shard map. This resolves the overloaded word "namespace".
- **Ingestion**: look up the profile; for a sharded family, place the event in
  the per-partition leaf shard and ensure the parent index pod + shard map exist.
  Unknown namespaces fall back to trait-based classification (the
  storage-strategy decision function) as a single unsharded pod.
- **Routing**: `health_check` / `metrics_query` → the live-state shard for the
  requested namespace (fan out across all shards if unspecified/missing);
  `cert_check` → `k8fy.certificates`. **Index pods are never returned** to the
  fetch step — they have no backend data (`passthrough`).

**Store-type substitutions** (until the backends exist): `k8fy.events` uses
`relational` instead of a log/search index; pod restart-count "metrics" are
folded into `k8fy.live-state` (current-state KV) rather than a separate TSDB
`k8fy.metrics` pod.

## Consequences

- **Positive:** ingestion and routing now share one taxonomy; "health of service
  X in `prod`" hits exactly one shard; index/shard structure follows ADR 0002 so
  the refinement loop can split/merge shards later without router changes.
- **Negative / cost accepted:** profiles are hardcoded in Go for the MVP (not yet
  runtime-registered or loop-tuned); the events/metrics store substitutions
  diverge from storage-strategy until a log-search and TSDB backend are wired;
  the index pod's shard map is updated with a non-transactional read-modify-write
  (acceptable at MVP volume, flagged for the refinement loop).
- **Revisit if:** we add the log-search/TSDB backends (restore `k8fy.events`/
  `k8fy.metrics` to their policy stores), or when integrations need to register
  profiles at runtime rather than in code.

## Retrieval keying (implemented as a follow-up)

Current-state events now carry an `entity_key` (the adapter sets it to the
pod/service/secret name). The ingester keys KV storage on it
(`podID:<entity_key>`) so the latest event for an entity overwrites the previous
one (latest-wins); history stores (relational) still key by the unique event ID.
On the read side, `redis.Query` does a point lookup when a caller supplies an
explicit entity key and otherwise **scans the whole shard** (`podID:*`), so a
query returns every entity in the namespace for the agent to reason over. This
closes the ingest→store→query→fetch loop for the live-state path.

Still open: **entity resolution** — mapping a fuzzy service name in a user's
question (e.g. "payment") to the exact stored keys (`payment-svc-abc123`). Today
that is left to the agent reasoning over the scanned shard; a dedicated
name→entities index could be added later if scans get expensive.
