# K8fy Adapter

## Overview

The K8fy adapter is the integration point between Kubernetes and agentify. It:
1. Watches Kubernetes API for pod/service changes
2. Scrapes metrics (restarts, resource usage)
3. Checks certificates for expiry
4. Normalizes to canonical events
5. Sends to backend for ingestion

## Components

### Watcher
- `src/adapters/k8fy/watcher.go` — K8s API client
- Methods:
  - `WatchPods()` — stream pod events (ADDED, MODIFIED, DELETED)
  - `WatchServices()` — stream service events
  - `ScrapeMetrics()` — periodic pod metrics collection
  - `ScrapeCertificates()` — periodic cert expiry checks

### Normalizer
- `src/adapters/k8fy/normalizer.go` — K8s → canonical events
- Functions:
  - `NormalizePodEvent()` — pod with phase, ready status, restarts
  - `NormalizeServiceEvent()` — service with endpoints
  - `NormalizeCertificateEvent()` — cert with expiry date
  - `ParseCertExpiry()` — extract cert NotAfter

### Configuration
- `src/adapters/k8fy/config.go` — env-based config:
  - `BACKEND_URL` — agentify backend address
  - `K8S_NAMESPACE` — namespace to watch (default: "default")
  - `SCRAPE_INTERVAL` — metrics scrape cadence (default: 30s)
  - `CERT_CHECK_INTERVAL` — cert check interval (default: 300s)

### Entry Point
- `src/adapters/k8fy/main.go` — starts watchers

## Deployment

### Dockerfile
- Multi-stage build (Alpine)
- Runs inside k8s cluster (in-cluster auth)

### Kubernetes manifests
- `infra/kubernetes/k8fy-adapter.yaml`:
  - Deployment (1 replica)
  - ServiceAccount with cluster-wide permissions
  - ClusterRole (read pods, services, secrets, events)

### Running locally (for testing)
```bash
# Set backend URL
export BACKEND_URL=http://localhost:8080
export K8S_NAMESPACE=default

# Run
cd src/adapters/k8fy
go run main.go
```

### Running on EKS
```bash
# Deploy
kubectl apply -f infra/kubernetes/k8fy-adapter.yaml

# Check logs
kubectl logs -n agentify -l app=k8fy-adapter -f
```

## Event Flow

```
Watcher observes K8s
  ├─ Pod restarted
  ├─ → NormalizePodEvent()
  ├─ → {event_namespace: "k8fy.live-state", type: "MODIFIED", payload: {...}}
  └─ → POST /api/ingest

Backend ingestion
  ├─ Classify: "time-range-scan" + "append-only" → "logs" store
  ├─ Route: k8fy.events pod
  ├─ Store: Postgres/Elasticsearch
  ├─ Update: pod registry
  └─ Emit: refinement loop observation
```

## Data Examples

### Pod event
```json
{
  "event_namespace": "k8fy.live-state",
  "type": "MODIFIED",
  "source": "kubernetes-api",
  "timestamp": "2026-05-31T10:00:00Z",
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
  "timestamp": "2026-05-31T10:00:00Z",
  "payload": {
    "secret": "tls-cert-prod",
    "namespace": "prod",
    "expires_at": "2026-07-15T10:00:00Z",
    "days_until_expiry": 45
  }
}
```

## Next: Sending events to backend

Currently, normalized events are logged but not sent to backend. To complete:

1. Create HTTP client (with retry + backoff)
2. Send events to `POST /api/ingest`
3. Handle errors (log, dead-letter queue)
4. Add metrics (events/sec, errors/sec)
