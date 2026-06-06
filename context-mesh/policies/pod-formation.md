# Policy: Pod Formation & Lifecycle

> **Question this answers:** When does a pod get *born*, *split*, *merge*, or
> *retire*? Pods are emergent — the system decides their existence from the data,
> not you. This policy defines the rules it follows.
>
> One of the four "brain" policies.

## Principle

_<one sentence, e.g. "A pod should represent a coherent slice of data that is
queried together; keep pods cohesive and loosely coupled.">_

## Lifecycle states

`forming → active → (splitting | merging | draining) → retired`

## Birth — when to spawn a new pod

Triggers to consider:
- [ ] Incoming events don't fit any existing pod (low similarity / new topic / new source).
- [ ] An existing pod exceeds a size/volume threshold.
- [ ] A distinct new access pattern emerges.
- _<your trigger?>_

**Cold-start question:** what is the *very first* pod? (Often: one catch-all pod
that later splits as patterns emerge.)

## Split — when one pod becomes many

- [ ] Pod grew too large for efficient query.
- [ ] Two clearly separable sub-topics/clusters detected inside it.
- [ ] Mixed access patterns hurting performance.
- **How is the split point chosen?** _<clustering? time boundary? tag boundary?>_

## Merge — when many pods become one

- [ ] Two pods are queried together so often they should co-locate.
- [ ] A pod is too small to justify its overhead.
- **Conflict handling on merge:** _<schema reconciliation, dedup?>_

## Retire — when a pod goes away

- [ ] No queries for N days AND data is cold.
- [ ] Superseded by a merged/split pod.
- **Disposition:** _<archive to cold store? summarize then delete? hard delete?>_

## Index pods & sub-pods (hierarchical / recursive mesh)

When a single pod holds too much data (or too-hot data), it doesn't grow without
bound — it becomes an **index over sub-pods**. The mesh pattern is **recursive**:
a pod can itself be a tiny mesh (a little index on top, shards underneath). This
adds no new concept — the orchestrator↔pod routing and [correlation](correlation.md)
rules apply unchanged, one level down.

### Two roles a pod can take

| Role | Holds | Job |
|------|-------|-----|
| **Index pod** (parent) | a small **shard map** — *no actual data* | route a query to the right child shard(s) |
| **Leaf pod** (shard) | the actual data | answer queries for its slice |

The index pod stays tiny and fast because it holds only the map, not the data —
that's what lets it scale.

### Choosing the partition key (the make-or-break decision)

Partition by the **dimension queries filter on most**, so the *common* query
touches exactly **one** shard and fan-out is the exception, not the rule. A wrong
key turns every query into a slow scatter-gather across all shards.

- _<for K8fy live-state: partition by `namespace` — "health of service X" hits one shard.>_
- _<other candidates: by cluster (multi-cluster), by resource type, by time window.>_

### Cross-shard queries

When a query spans shards (e.g. "unhealthy services across all namespaces"), the
index pod fans out and combines results using the existing
[correlation](correlation.md) rules — same behavior as the top-level orchestrator.

### Splitting / merging shards is just these same rules, applied inside a pod

- Shard too big or too hot → **split** (reuse the Split rules above).
- Two tiny, rarely-queried shards → **merge**.
- The index pod simply updates its shard map; the [refinement-loop](refinement-loop.md)
  drives this automatically from size/heat signals.

### Nuance for pass-through pods

For a **persisted** pod (e.g. `k8fy.events`, `k8fy.metrics`) the shard map points
to **data at rest**. For a **pass-through** pod (e.g. `k8fy.live-state`, queried
live from the K8s API) the same map instead points to **live sources to query**
(namespace/cluster → which endpoint to call). Same structure, different leaves.

## Invariants (things that must always hold)

- _<e.g. "Every event belongs to exactly one active pod" — or do you allow
  overlap? Decide and record in an ADR.>_
- _<e.g. "The pod-registry must always reflect reality within X seconds.">_

## How this policy adapts over time

- _<thresholds learned from query stats? cross-ref [refinement-loop](refinement-loop.md).>_

## Worked example — K8fy lifecycle

K8fy integrates Kubernetes signals and generates four pods (see
[storage-strategy.md](storage-strategy.md#worked-example--k8fy-operational-use-case)).
Here's when each is born, split, merged, or retired:

| Pod | Birth | Split | Merge | Retire |
|-----|-------|-------|-------|--------|
| `k8fy.live-state` (index) | K8fy integration enabled | A namespace shard grows > 1000 pods OR query latency > threshold | Namespace shard silent for 7d + pod count < 100 | Namespace is deleted in K8s |
| `k8fy.live-state` shards (children) | First query/API call to a namespace | One shard too many pods or too hot → split by cluster or resource type | Two small shards (< 50 pods each, low query rate) | Namespace deleted |
| `k8fy.certificates` | K8fy integration enabled | Unlikely — table is small; only if 10k+ certs | N/A | Never (always live) |
| `k8fy.events` | K8fy integration enabled | Time-window based: split into weekly/monthly boundaries as data grows | Old time-windows < 7d old archived together | After retention (30d) — move to cold storage, then delete |
| `k8fy.metrics` | K8fy integration enabled | Time-window based: split into monthly boundaries | Old months compressed/merged into archives | After retention (90d) — move to cold storage, then delete |

**Key decisions embedded here:**
- The **index pod** (`k8fy.live-state`) is born once; its **child shards** are born on-demand
  per namespace.
- **Split thresholds** are pod count (for state) and time-window boundaries (for
  append-only). They're learned from real usage — conservative at first, tuned by
  [refinement-loop](refinement-loop.md).
- **Merge** happens quietly after a time-based silence window (7 days), not
  immediately — to avoid thrashing if a namespace quiets temporarily.
- **Retention** is a hard boundary (30d for events, 90d for metrics), with cold
  tiering before final deletion.

## Open questions

- [ ] Is pod formation automatic, or human-approved for the first version?
- [ ] How are in-flight queries handled while a pod is splitting/merging?
- [ ] Upper bound on number of pods before routing itself gets expensive?
