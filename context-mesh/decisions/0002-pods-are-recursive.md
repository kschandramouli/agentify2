# 0002 – Pods are recursive: large pods become an index over sub-pod shards

## Status

Accepted   ·   2026-05-30

## Context

A single pod can accumulate more data (or more query heat) than is efficient to
store or scan in one place — e.g. `k8fy.live-state` across many namespaces. We
need a way to partition a pod's data while still letting the orchestrator route,
access, and correlate as if it were one logical pod, and we want this to happen
dynamically rather than being designed up front.

## Decision

Make the mesh **recursive**. A pod can take one of two roles:

- **Index pod** — holds only a small **shard map** (no data); routes a query to
  the right child shard(s).
- **Leaf pod** — holds the actual data for one slice.

The same orchestrator routing and [correlation](../policies/correlation.md) rules
apply one level down; the same [pod-formation](../policies/pod-formation.md)
split/merge rules drive sharding automatically via the
[refinement-loop](../policies/refinement-loop.md). Pods are partitioned by the
**dimension queries filter on most** so the common query is single-shard.

The pod-registry gains `kind` (`index|leaf`), `partition_key`, and `shards`.
For pass-through pods the shard map points to **live sources**; for persisted
pods it points to **data at rest**.

## Consequences

- **Positive:** pods scale without bound; partitioning is dynamic and reuses
  existing rules (no new concepts); index pods stay tiny and fast.
- **Negative / cost accepted:** cross-shard queries cost a fan-out + correlate;
  a poorly chosen partition key turns common queries into slow scatter-gather;
  the shard map is another piece of state to keep consistent.
- **Revisit if:** pods stay small enough in practice that recursion is unused
  overhead, or if a different sharding mechanism (e.g. the underlying store's own
  partitioning) makes the index-pod layer redundant.
