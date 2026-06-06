# 0001 – Adopt a self-organizing context-mesh architecture

> Starter ADR recording the foundational decision. Edit to match reality.

## Status

Accepted   ·   2026-05-29

## Context

The product needs to store streamed data events and answer user queries against
them. We do not know the optimal data partitioning upfront and want the system to
discover it from real-time data and query patterns, rather than hardcoding a fixed
schema or storage layout.

## Decision

Adopt a **context-mesh** architecture composed of:
- Independent, emergent **pods** (each owning a coherent slice of data in whatever
  store type best fits it),
- A primary **orchestrator** that routes queries and correlates across pods,
- Four governing **policies** (storage-strategy, pod-formation, refinement-loop,
  correlation) that define behavior in English first, implemented in `src/`.

Design-time context (policies/specs/ADRs) lives in `context-mesh/` under version
control. Runtime pod structure lives in a self-maintained `pod-registry`, not in
the repo.

## Consequences

- **Positive:** storage layout adapts to real usage; no premature schema lock-in; behavior is specified in reviewable English before code.
- **Negative / cost accepted:** more moving parts than a single fixed database; need safeguards against refinement "thrashing"; routing cost grows with pod count.
- **Revisit if:** data volume/access patterns turn out stable enough that a fixed schema would be simpler and cheaper.
