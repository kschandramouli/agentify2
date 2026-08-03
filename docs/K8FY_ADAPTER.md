# K8fy Adapter

> Rewritten 2026-08-03 — the previous version described a Go implementation
> with `.go` file extensions and said "normalized events are logged but not
> sent to backend." Neither is true: the adapter has been Python since early
> on, and it has fully emitted to `/api/ingest` for a long time — this
> version matches `src/adapters/k8fy/` as it stands today.

## Overview

The K8fy adapter is the original, still-running integration point between
Kubernetes and agentify's ingested-data store. It:
1. Watches the K8s API (pods, services, Deployments) via long-lived watch
   streams — not polling
2. Scrapes container restart counts and TLS certificate expiry on a timer
3. Normalizes everything to agentify's canonical `Event` schema
4. Emits to the backend's `POST /api/ingest`
5. Serves on-demand pod logs and namespace discovery over its own small HTTP endpoint

**Relationship to Discovery (`agentify-discovery`):** this adapter and
Discovery (ROADMAP P18, see `docs/AGENT_INTEGRATION.md`) are two separate
components that both run **per cluster**, with overlapping concerns — both
need K8s RBAC read access inside that cluster, both push data outbound to
**the Hub** (this adapter to `/api/ingest`; Discovery to
`/api/cluster-inventory`/`/api/service-dependencies`/the persistent
connection). Neither one runs anywhere near the Hub itself; both are purely
in-cluster, outbound-only pushers. [ADR 0022](../context-mesh/decisions/0022-multi-tenant-fleet-hub.md)
Decision #9 flags having two such components as a real smell whose natural
end state is one per-cluster deployable doing both jobs — explicitly **not
resolved**, since merging them means migrating every existing adapter
deployment.

## Components (all Python, `src/adapters/k8fy/`)

### Watcher (`watcher.py`) — `K8sWatcher`
- `watch_pods()` / `watch_services()` / `watch_deployments()` — long-lived
  `kubernetes.watch.Watch()` streams, auto-retrying with a 2s backoff on
  error. Deployment watches dedupe by `deployment.kubernetes.io/revision`
  annotation so only genuine rollouts emit a change event (spec 007).
- `scrape_metrics(interval)` — periodically lists every pod, emits a restart-count sample per container.
- `scrape_certificates(interval)` — periodically lists Secrets, filters to `type=kubernetes.io/tls`, parses each with `cryptography.x509` (expiry + SAN/CN DNS names), emits a cert-expiry event.
- `list_namespaces()` — powers namespace/service discovery for the frontend autocomplete.
- **Cluster-wide by default**: `K8S_NAMESPACE=*` (or empty) watches every namespace except a fixed skip-list (`kube-system`, `kube-public`, `kube-node-lease`, `cert-manager`, `monitoring`, `ingress-nginx`) — new namespaces are picked up automatically, no restart needed. A single namespace can still be pinned via `K8S_NAMESPACE`.

### Normalizer (`normalizer.py`)
- Builds the canonical event dict `POST /api/ingest` expects — `id`, `timestamp`, `event_namespace`, `type`, `source`, `payload`, `traits`, `entity_key`. The trait values (not the payload) are what the backend's storage-strategy classification actually keys on.
- `normalize_pod_event` / `normalize_service_event` / `normalize_deploy_event` / `normalize_metric_event` / `normalize_certificate_event`.

### Emitter (`emitter.py`) — `Emitter`
- Posts to `POST {BACKEND_URL}/api/ingest`. Best-effort: a failed POST is logged and swallowed, never crashes a watch loop.
- **Already sends `Authorization: Bearer {BACKEND_AUTH_TOKEN}` on every request** — this predates [ADR 0024](../context-mesh/decisions/0024-ingested-data-cluster-scoping.md), but that token was never checked server-side until ADR 0024 wired `HandleIngestEvent` to call `resolveTenantContext`. **To join a multi-cluster fleet**, set this adapter's `BACKEND_AUTH_TOKEN` to the same value as the `Integration` row's `collector_token` (minted the same way `agentify-discovery`'s `COLLECTOR_TOKEN` already is) — no adapter code change needed, only configuration.

### Log server + namespace discovery (`logserver.py`)
- `POST /logs` (`spec 008` / [ADR 0014](../context-mesh/decisions/0014-on-demand-ephemeral-log-fetch.md)) — the backend's on-demand pod-log fetch calls this; logs are read live and never persisted by the adapter. Bearer-guarded via `ADAPTER_AUTH_TOKEN` (constant-time compare); open if unset (dev only).
- Namespace discovery endpoint backing the frontend's "known namespaces" autocomplete.

### Configuration (`config.py`)
| Env var | Purpose | Default |
|---|---|---|
| `BACKEND_URL` | Ingestion target | `http://localhost:8080` |
| `BACKEND_AUTH_TOKEN` | Bearer sent on every `/api/ingest` POST (empty = unauthenticated push, single-cluster default) | `""` |
| `K8S_NAMESPACE` | `*`/empty = cluster-wide; a value pins to one namespace | `*` |
| `SCRAPE_INTERVAL` | Metrics scrape cadence (seconds) | `30` |
| `CERT_CHECK_INTERVAL` | Cert scrape cadence (seconds) | `300` |
| `LOG_SERVER_PORT` | On-demand log endpoint port | `8200` |
| `MAX_TAIL_LINES` | Hard cap on log tail length | `200` |
| `ADAPTER_AUTH_TOKEN` | Guards `POST /logs` (empty = open, dev only) | `""` |

### Entry point (`main.py`)
Loads in-cluster kube config (falls back to local kubeconfig for dev),
starts every watcher/scraper/log-server as a daemon thread, blocks on
`SIGINT`/`Ctrl-C`.

## Deployment

### Kubernetes manifests (`infra/kubernetes/k8fy-adapter.yaml`)
- Deployment (1 replica), Service (log-server port 8200)
- ServiceAccount with IRSA annotation
- ClusterRole: `list/watch/get` on `pods`, `services`, `secrets`, `events`, `namespaces`; `get` on `pods/log`; `list/watch/get` on `apps/deployments`

### Running locally
```bash
export BACKEND_URL=http://localhost:8080
export K8S_NAMESPACE=default   # or leave unset/"*" for cluster-wide

cd src/adapters   # k8fy.main uses relative imports, so run as a package from here
python -m k8fy.main
```

### Running on EKS
```bash
kubectl apply -f infra/kubernetes/k8fy-adapter.yaml
kubectl logs -n agentify -l app=k8fy-adapter -f
```

## Event flow

```
[this cluster] K8sWatcher observes K8s (watch stream or scrape tick)
  ├─ normalizer.normalize_*_event() → canonical dict
  └─ Emitter.emit() → POST /api/ingest (Bearer BACKEND_AUTH_TOKEN)  ─────▶  crosses into the Hub

[the Hub] ingestion (see EVENT_INGESTION.md) — none of this runs in the cluster
  ├─ resolveTenantContext(r) → (tenant_id, cluster_id)
  ├─ Classify traits → store type (kv for live-state/certs, relational for events/metrics)
  ├─ Route to a cluster-aware pod ID (models.PodID, ADR 0024)
  ├─ Store in Postgres
  └─ Update pod registry
```

## Data examples

### Pod event
```json
{
  "event_namespace": "k8fy.live-state",
  "type": "MODIFIED",
  "source": "kubernetes-api",
  "entity_key": "payment-svc-abc",
  "timestamp": "2026-08-03T10:00:00Z",
  "payload": {
    "pod_id": "payment-svc-abc",
    "namespace": "prod",
    "phase": "Running",
    "ready": true,
    "restarts": 2
  }
}
```

### Certificate event
```json
{
  "event_namespace": "k8fy.certificates",
  "type": "cert_check",
  "source": "kubernetes-api",
  "entity_key": "tls-cert-prod",
  "timestamp": "2026-08-03T10:00:00Z",
  "payload": {
    "secret": "tls-cert-prod",
    "namespace": "prod",
    "expires_at": "2026-10-15T10:00:00Z",
    "dns_names": ["payment.prod.svc.cluster.local"]
  }
}
```
