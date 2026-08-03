# Glossary

> Keep domain + architecture terms here so naming stays consistent across specs,
> code, and conversations with Claude. Add a line whenever a new term appears.

| Term | Definition |
|------|------------|
| **Context-mesh** | The overall architecture: a network of independent pods queried and correlated by an orchestrator. Two layers — *design-time* (this repo) and *runtime* (live data). |
| **Pod** | A self-contained unit of stored data owning a coherent slice, in whatever store type best fits it. Emergent — created/refined by the system, not predefined. |
| **Orchestrator** | The "primary pod" / front door: interprets a query, routes to pod(s), correlates results, returns the answer. |
| **Pod-registry** | The runtime, self-maintained map of all pods (what each owns, freshness, stats, lifecycle). The orchestrator's source of truth for routing. |
| **Policy** | An English-first rule set governing behavior. The four: storage-strategy, pod-formation, refinement-loop, correlation. |
| **Refinement loop** | The feedback cycle that reshapes storage/routing based on streamed events and query outcomes. |
| **Event** | A unit of incoming data streamed into the mesh and stored in a pod. |
| **Correlation** | Combining results from multiple pods into one coherent answer. |
| **Tenant** | A customer/org owning a fleet of one or more clusters (ADR 0022). Rows in tenant-scoped tables (`service_dependencies`, `cluster_services`) carry `tenant_id`; single-deployment installs default to `DefaultTenantID`. |
| **Fleet cluster** | One of a tenant's Kubernetes clusters, represented by an `Integration` row (`ID` doubles as `cluster_id`). A tenant can own several. |
| **Fleet collector (`agentify-discovery`)** | A deterministic, non-agentic, per-cluster Deployment (ADR 0022 Decision #6) that mines namespace/service inventory and a service-dependency graph, pushes them to the Hub, and holds a persistent outbound connection for on-demand live reads. Never runs Claude/LLM calls. |
| **CollectorToken** | The bearer credential a fleet collector (or, since ADR 0024, a k8fy-adapter) presents when pushing to the Hub — resolved server-side via `resolveTenantContext` to `(tenant_id, cluster_id)`. Distinct from `Integration.Token` (the Hub's outbound credential *to* an adapter) — conflating the two directions would let a leaked outbound token grant inbound push access. |
| **`resolveTenantContext`** | The Go backend helper (`handlers.go`) every collector-facing endpoint calls: absent credential → `(DefaultTenantID, "")` (today's single-cluster behavior, unchanged); unrecognized credential → rejected (401); valid `CollectorToken` → that `Integration`'s `(tenant_id, cluster_id)`. |
| **Service→cluster resolver** | `resolve_service_clusters(namespace, service, backend_url)` (agent) / `GET /api/resolve-cluster` (Hub) / the `cluster_services` registry table (ADR 0023) — answers "which fleet cluster(s) run this service?" from data the collector's inventory push already gathers. 0/1/N matches; N is surfaced to the diagnosing skill as a fan-out, never silently resolved to one. |
| **Live drill-down / `CollectorHub`** | The Hub-side connection registry (`internal/api/collector_hub.go`) and `POST /api/live-fetch` relay (ROADMAP P18 use case #9, ADR 0022 Decision #7) — lets the agent ask a specific fleet cluster's collector for a real-time K8s read (pods, logs, events, certs) over its already-open outbound connection, without the Hub ever holding standing per-cluster credentials. |
| **Cluster-aware pod ID (`models.PodID`)** | ADR 0024's fix for ingested-data (`current_state`/`events`) isolation: a pod ID optionally embeds `cluster_id` (`"k8fy.live-state.cluster-42.payments"` vs. the legacy `"k8fy.live-state.payments"`). Isolation is achieved by which pod ID a query targets, not by a tenant WHERE-clause/RLS on those two tables — deliberate, see ADR 0024's "Isolation mechanism" section. |
| **Pattern A** | A skill strategy (spec 010, ADR 0017): deterministic parallel pre-fetch of every predictable signal, then exactly one Claude call — no agentic tool-calling loop. All core K8fy skills (`HealthSkill`, `CertAuditSkill`, `DiagnoseSkill`, `ChangeHistorySkill`, `RestartTrendSkill`) use this; only the `K8fyAgent` fallback still runs an agentic loop (Pattern B). |
| _<your domain term>_ | _<definition>_ |
