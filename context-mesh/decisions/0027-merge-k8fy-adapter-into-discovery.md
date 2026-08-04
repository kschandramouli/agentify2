# 0027 – Merge k8fy-adapter into agentify-discovery

## Status

Accepted · (date: 2026-08-04)

## Context

[ADR 0022](0022-multi-tenant-fleet-hub.md) Decision #9 named the overlap
between two per-cluster collectors as a real smell but deliberately deferred
it: `src/adapters/k8fy/` (the original collector — event-driven K8s watch
streams via the `kubernetes` client library, periodic metric/certificate
scrapes on separate timers, and an inbound HTTP log-server on port 8200,
POSTing canonical events to `/api/ingest` under `BACKEND_AUTH_TOKEN`) and
`src/adapters/discovery/` (`agentify-discovery` — the newer fleet collector
built for [ADR 0022](0022-multi-tenant-fleet-hub.md)'s multi-tenant model:
asyncio-only, deliberately avoids the `kubernetes` client library for API
version-skew control, runs a fixed-interval scan cycle plus a persistent
outbound live-relay connection under `COLLECTOR_TOKEN`). Both ran per
cluster, both needed K8s RBAC read access, both reported state to the same
Hub. Running them side by side meant two RBAC ServiceAccounts, two
Deployments, two credential types, and two implementations of overlapping
K8s-watching logic per cluster — a real operational and cognitive cost with
no corresponding benefit once Discovery's scan cycle had grown broad enough
to plausibly absorb everything k8fy-adapter did.

## Decision

Fully merge k8fy-adapter's capabilities into agentify-discovery and retire
k8fy-adapter as a standalone deployable entirely — not a staged,
additive-then-retire-later migration. Concretely:

- **Watch streams**: a new async watch primitive
  (`discovery/k8s_client.py`'s `watch_resource`, an `httpx`-based async
  generator over K8s's `?watch=1` streaming API) replaces the `kubernetes`
  client library's blocking `Watch().stream()`. New `discovery/watch.py`
  runs pod/service/deployment watch loops (with the same reconnect/backoff
  discipline as `live_relay.py`) alongside Discovery's existing scan-cycle
  and live-relay tasks. Deployment watches keep k8fy's
  `deployment.kubernetes.io/revision`-annotation dedup (spec 007) so only
  genuine rollouts emit a change event.
- **Metric and certificate scraping** fold into Discovery's existing scan
  cycle as two new steps (`_scan_metrics`, `_scan_certificates` in
  `main.py`) rather than keeping k8fy's separate `SCRAPE_INTERVAL`/
  `CERT_CHECK_INTERVAL` timers — one ticker (`SCAN_INTERVAL_SECONDS`) now
  drives inventory, ingress, health, metrics, and certificates together,
  consistent with [ADR 0022](0022-multi-tenant-fleet-hub.md) Decision #6's
  "not split into a separate CronJob" philosophy. Default cadence coarsens
  from k8fy's 30s to Discovery's 60s; configurable if a faster default
  turns out to matter.
- **Event normalization** (`discovery/normalize.py`, new) ports k8fy's
  `normalizer.py` pure functions near-verbatim, adapted to read raw K8s API
  dicts instead of `kubernetes` client typed objects. The `k8fy.*`
  event-namespace strings (`k8fy.live-state`, `k8fy.events`, `k8fy.metrics`,
  `k8fy.certificates`) are **not** renamed — that's the stable K8fy
  pod-mesh taxonomy ([ADR 0005](0005-k8fy-pod-taxonomy-realization.md)),
  baked into `deriveStoreType`, `RouteToPods`, and every existing Postgres
  row. Only the adapter *component* is renamed/merged, not the data
  vocabulary it writes into.
- **The inbound log-server is retired outright, not ported.** k8fy-adapter
  ran its own HTTP server (port 8200, `POST /logs`) that the Hub's
  `AdapterClient.FetchLogs` called synchronously per log request. Discovery
  already has an equivalent capability through a fundamentally better
  transport: `live_get_pod_logs`, relayed over its existing persistent
  outbound connection (no inbound port to expose, no separate auth path).
  `get_logs`'s router already prefers this path over any cached/adapter
  source. `AdapterClient`, `HandleAgentFetch`'s `get_pod_logs` special
  case, and the agent's `get_pod_logs` tool schema are deleted, not
  deprecated — `get_logs`/`live_get_pod_logs` fully cover the capability.
- **Credentials unify onto `COLLECTOR_TOKEN`/`agentify-discovery-secret`**
  for everything, including the fine-grained pod/service/deploy events that
  previously went to `/api/ingest` via k8fy's separate `BACKEND_AUTH_TOKEN`.
  `resolveTenantContext`'s existing tolerance for an absent credential on
  `/api/ingest` (defaulting to `DefaultTenantID`) is preserved unchanged —
  a single-cluster deployment with `COLLECTOR_TOKEN` unset keeps ingesting
  exactly as before; only Discovery's *other* pushes (inventory/ingress/
  health/deps) and its live-relay connection attempt require a valid
  token, same as today.
- **`SeedNamespaceCache` (the Hub's "poll the adapter for up to 5 min at
  startup" workaround) is retired outright**, not repointed. K8s watch
  semantics mean establishing a watch performs an initial LIST first,
  emitting every existing object as an `ADDED` event — so once the merged
  Discovery's watch stream is running, `current_state` self-populates
  within the first watch cycle. The three other `AdapterClient.
  DiscoverNamespaces` call sites (`HandleSyncNamespaces`,
  `HandleTrackedEntities`'s empty-state fallback) are repointed to a new
  `ListClusterServices` Postgres method — a `SELECT DISTINCT namespace,
  service FROM cluster_services` read of data Discovery's existing
  periodic inventory push already keeps fresh — preserving both handlers'
  external HTTP contract (same URL, same JSON response shape) while
  removing the live adapter call entirely.
- RBAC, Terraform (ECR repo, IRSA role, Secrets Manager secret), CI
  workflows, and frontend copy are all updated to match: k8fy-adapter's
  manifest, IRSA role/policy, and `agentify/dev/adapter` Secrets Manager
  secret are deleted; Discovery's ClusterRole gains `watch` on exactly the
  resources the ported watchers use (pods, services, deployments) — not a
  blind union of k8fy's grant, which included unused `watch` on secrets/
  events no watcher method ever consumed.

## Consequences

- **Positive:** one per-cluster deployable, one RBAC ServiceAccount, one
  credential type, one thing to operate per cluster — resolves [ADR
  0022](0022-multi-tenant-fleet-hub.md) Decision #9 as originally framed.
  `current_state` self-bootstraps from the watch stream's initial LIST,
  eliminating an entire class of "adapter not ready yet" startup-race
  workaround (`SeedNamespaceCache`) outright rather than papering over it.
- **Negative / cost accepted:** metric/certificate scrape cadence coarsens
  from k8fy's dedicated 30s/300s timers to Discovery's shared
  `SCAN_INTERVAL_SECONDS` (default 60s) — acceptable since nothing in the
  product depends on sub-minute freshness for restart counts or cert
  expiry. k8fy's "pin to one namespace" config option (`K8S_NAMESPACE`) is
  dropped entirely rather than ported — Discovery's cluster-wide-with-
  exclude-list model is already the superset, and running two different
  namespace-scoping mechanisms in one merged component would be confusing,
  not simplifying. No live cluster was available to validate the
  watch-stream reconnect/backoff behavior or the self-bootstrapping
  `current_state` claim against real K8s API behavior — this is a design
  decision made and tested against the K8s API's documented watch
  semantics and Discovery's existing test conventions, not live-validated.
- **Revisit if:** a customer needs sub-minute metric/certificate freshness
  (reintroduce a separate fast-cadence step, not a whole second timer
  system); the coarser default cadence causes noticeable staleness
  complaints in practice.
