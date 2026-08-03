# 0024 – Ingested-data (current_state/events) cluster scoping

## Status

Accepted · (date: 2026-08-03)

## Context

ADR 0023 built the service→cluster resolver and cluster-scoped the *live*
on-demand path (`live_list_pods` etc., relayed through `agentify-discovery`'s
persistent connection). It explicitly deferred the other half: the
*ingested* data path — `current_state`/`events`, which backs
`get_service_health`, `get_pod_events`, `get_certificates`, `query_pod`,
`get_metrics_history`, `get_change_history` — had zero tenant/cluster
scoping. This ADR closes that gap.

Confirmed by direct code inspection before designing this:

- **Pod-ID collision, exact and deterministic.** `routeAndCreatePod`
  (`ingestion/ingester.go`) and `routeLiveState`
  (`orchestrator/query_executor.go`) both built pod IDs as
  `"k8fy.live-state." + namespace` — no cluster component. Two clusters both
  running a `payments` namespace produced the *identical* pod ID, so their
  data landed in and was read from the same row set.
- **Worse for certs/events/metrics:** those profiles are `Sharded: false`
  (`config/pod_profiles.go`) — a single **fixed, global** pod ID
  (`"k8fy.certificates"`, `"k8fy.events"`, `"k8fy.metrics"`) shared by every
  namespace *and every cluster*.
- **`models.Pod`, `models.Event`, the DynamoDB registry key, and
  `PodFilter` had no tenant/cluster dimension at all** — not a
  "add a WHERE clause" fix; the data model itself had nowhere to put it.
- **`HandleIngestEvent` never called `resolveTenantContext`** — but
  `src/adapters/k8fy/emitter.py` (the existing K8s adapter) **already sends
  a Bearer token** with every ingest POST (`BACKEND_AUTH_TOKEN`, unused
  server-side until this ADR). The transport already existed; only the
  Hub-side check was missing.
- **`HandleAgentFetch` extracted no cluster identity** from the agent's
  request at all.

## Decision

**Cluster-aware pod IDs, not RLS, is the isolation mechanism** for
`current_state`/`events`.

**`models.PodID(parts ...string) string`** (`internal/models/shard.go`)
joins non-empty parts with `.`. Empty `clusterID` reproduces today's shape
exactly (`"k8fy.live-state.payments"`); non-empty inserts a segment
(`"k8fy.live-state.cluster-42.payments"`, or `"k8fy.certificates.cluster-42"`
for the single-global-pod families). One function covers both the
namespace-sharded and single-global-pod cases — this directly realizes
`pod-formation.md`'s own stated partition-key candidate ("by cluster
(multi-cluster)"), which the policy anticipated but never built.

**Why not RLS (unlike `service_dependencies`/`cluster_services`, ADR 0023):**
`Client.Query`/`CurrentState.Query` already filter strictly by
`WHERE pod_id = $1`. A cluster-aware pod ID makes that filter already
correct — no additional tenant/cluster WHERE clause is needed on these two
tables. Enabling `FORCE ROW LEVEL SECURITY` on them instead would mean any
query that doesn't call `setTenantContext` first silently returns **zero
rows, not an error** — and `current_state`/`events` have many more call
sites than `service_dependencies` did (the retention janitor
`PurgeOlderThan`, `TrackedEntities`/frontend autocomplete, every existing
Tier-1 tool), none of which this task touches. Pod-ID scoping achieves the
same isolation without that silent-data-loss risk, and matches the mesh
architecture's own principle: pods *are* the partition/isolation unit.
`tenant_id`/`cluster_id` columns are still written on every row (via
`data["tenant_id"]`/`data["cluster_id"]` through the existing generic
`storage.Backend.Store(podID, data)` interface — no interface signature
change) for observability, but are not filtered on for correctness.

**Ingest side:** `HandleIngestEvent` now calls `resolveTenantContext(r)`.
Unlike the fleet-collector push endpoints, an **absent** credential is not
rejected — defaults to `(DefaultTenantID, "")`, so every existing
k8fy-adapter deployment (none of which have been given a `CollectorToken`
yet) keeps ingesting exactly as before. An **invalid** (unrecognized) token
is still rejected, same as every other `resolveTenantContext` consumer. A
fleet-onboarded k8fy-adapter needs no code change — just configuring its
existing `BACKEND_AUTH_TOKEN` with the same `CollectorToken` value already
minted for its `Integration` row.

**Read side:** `HandleAgentFetch` accepts an optional `cluster_id` in the
tool-call args — same explicit-arg shape `HandleLiveFetch` (ADR 0022
Decision #7) already uses, not a bearer credential (the agent presents
none — ADR 0022 Decision #8 already flags agent tenant-awareness as a
separate, unresolved follow-up, unchanged by this ADR). `RouteToPods` gained
a `clusterID` parameter threaded into `routeLiveState` and the
cert/metrics/change-history single-pod routing. The "diagnose"/default
fan-out routing (`HandleQuery`'s initial pod selection, not
`HandleAgentFetch`'s per-tool routing) is untouched — out of scope, per the
same boundary ADR 0023 drew.

**Agent wiring:** `DiagnoseSkill` and `HealthSkill` both call
`resolve_service_clusters` (ADR 0023) and, for every resolved cluster, add a
cluster-scoped `get_service_health` prefetch task alongside the existing
unscoped one and a live snapshot via `live_list_pods` (ADR 0022 Decision
#7) — per `correlation.md`'s existing fan-out rule (diagnostic intent fans
out across every matching signal, Tier-2 synthesizes/surfaces
disagreement). A no-op for the common case (no registered fleet clusters).

**New capability unlocked, `live_get_certificates` (ROADMAP P16/P18):**
`CertAuditSkill` gained the same resolver-based fan-out, but there was no
live equivalent of `get_certificates` to fan out to — so this ADR also
builds one:
- `agentify-discovery`'s ClusterRole gains `secrets: list, get` — **the
  first Secrets access this collector has ever been granted.** `k8s_client.
  list_tls_secrets` always applies a server-side
  `fieldSelector=type=kubernetes.io/tls`, so the grant only ever surfaces
  TLS secrets, never arbitrary ones — but RBAC itself cannot express that
  restriction; it's enforced by the client, not Kubernetes. Flagging this
  plainly: the ClusterRole itself grants list/get on **all** Secrets
  cluster-wide.
- `live_get_certificates` decodes only `tls.crt` (never reads `tls.key`)
  and returns only `{name, common_name, expiry_date, days_until_expiry}` —
  `days_until_expiry` computed deterministically in Python, never handed to
  Claude as arithmetic (same principle already established for the
  ingested-store cert-check flow). Raw cert/key bytes never appear in the
  response, by construction — the parsing helper only ever builds this one
  summary dict.
- Always requires an explicit `cluster_id` — there is no local
  (this-agent's-own-cluster) implementation, unlike the other `live_*`
  tools. New dependency: `cryptography` (X.509 parsing), added to
  `agentify-discovery`'s `requirements.txt`.

## Consequences

- **Positive:** closes P16's other, harder sub-problem (the ingested-data
  read path) without an RLS retrofit's silent-data-loss risk; zero behavior
  change for every deployment that hasn't registered a fleet cluster
  (`clusterID` empty everywhere → identical pod IDs to today); reuses
  existing plumbing throughout (the k8fy-adapter's already-sent bearer
  token, the existing `CollectorToken`/`resolveTenantContext` mechanism, the
  existing generic `Backend.Store(podID, data)` interface — no interface
  signature changes anywhere).
- **Negative / cost accepted:** `TrackedEntities` (frontend autocomplete)
  had to be patched to strip a cluster segment out of cluster-scoped pod
  IDs — otherwise a fleet cluster's data would show up as a bogus
  `"{clusterID}.{namespace}"` pseudo-namespace; this flattens away *which*
  cluster an entity came from in that one listing (acceptable — it's a flat
  autocomplete, not a per-cluster view). The `agentify-discovery` ClusterRole
  now has cluster-wide Secrets list/get, RBAC-unscoped to TLS type (enforced
  client-side only) — a real, if narrow, increase in blast radius if that
  collector's credentials were ever compromised.
- **Revisit if:** the RBAC-vs-client-enforced-narrowing gap above becomes
  a real concern (e.g. a stricter customer requires Kubernetes-native
  enforcement) — would need a validating admission policy or per-secret
  RBAC, neither of which exists today; `h.adapterClient` remaining a
  single global (P16 sub-problem 2, still not addressed) becomes the active
  blocker for something.
