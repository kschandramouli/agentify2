# Testing Guide

## Overview

Testing is done in 3 layers:
1. **Unit tests** — fast, in-memory, isolated logic
2. **Integration tests** — real services (Postgres, Redis, Weaviate), full flows
3. **Manual tests** — curl examples for hands-on verification

For MVP, we focus on **integration tests** (the most valuable) with mock data.

---

## Quick Start

### Prerequisites
- Docker & Docker Compose
- Go 1.21+
- `jq` (for pretty-printing JSON)

### Run all tests
```bash
# Full integration test suite (starts services, runs tests, stops services)
make test

# Or run the test script directly
bash tests/run.sh
```

### Run individual test layers

**Start test services (keeps running)**
```bash
make test-up

# Services available:
# - Postgres: localhost:5433 (DB: agentify_test, user: postgres, pwd: postgres)
# - Redis: localhost:6380
# - Weaviate: localhost:8081
# - DynamoDB Local: localhost:8000
```

**Run integration tests**
```bash
make test-integration
```

**Stop services**
```bash
make test-down
```

**View logs**
```bash
make test-logs
```

---

## Test Structure

### 1. Mock Data Generators (`tests/mocks/`)
Generate fake K8fy events without touching Kubernetes:

```go
gen := mocks.NewK8fyEventGenerator("prod")

// Generate a pod restart event
event := gen.GeneratePodRestartEvent("payment-svc-abc")

// Generate a healthy pod event
event := gen.GeneratePodHealthyEvent("api-server-123")

// Generate certificate expiry event
event := gen.GenerateCertificateExpiringEvent("tls-cert", 12)

// Bulk generate
events := gen.GenerateBulkPodEvents("test-pod", 100)
```

### 2. Test Fixtures (`tests/fixtures/`)
Seed the pod registry with initial pods:

```go
fixtures := fixtures.NewPodFixtures(registry, logger)

// Seed K8fy pods (live-state, events, certificates, metrics)
fixtures.SeedK8fyPods(ctx)

// Later, clean up
fixtures.ClearPods(ctx)
```

### 3. Integration Tests (`tests/integration/`)
Full flow tests with real services:

- `event_ingestion_test.go` — test ingestion pipeline
- `query_orchestration_test.go` — test query routing + fetching
- `storage_test.go` — test storage backends (Postgres, Redis, Weaviate)

Run:
```bash
make test-integration
```

### 4. Manual Testing (`tests/manual_test.sh`)
Real HTTP requests for hands-on verification:

```bash
# Start backend (in another terminal)
cd src/backend && go run cmd/agentify/main.go

# Make sure test services are running
make test-up

# Run manual tests
bash tests/manual_test.sh
```

---

## Test Data

### Event fixtures
Pre-made JSON events for testing (in `tests/testdata/`):
- `pod_restart_event.json` — a pod restart
- `certificate_event.json` — certificate expiry

Use with curl:
```bash
curl -X POST http://localhost:8080/api/ingest \
  -H "Content-Type: application/json" \
  -d @tests/testdata/pod_restart_event.json
```

---

## Example: Full Flow Test

```bash
# 1. Start services
make test-up

# 2. In another terminal, start the backend
cd src/backend
go run cmd/agentify/main.go

# 3. In a third terminal, run manual tests
cd tests
bash manual_test.sh

# Expected flow:
# ✓ Health check passes
# ✓ List pods (shows seeded K8fy pods)
# ✓ Ingest pod restart event
# ✓ Ingest certificate event
# ✓ Query for health (TODO: returns empty for now)

# 4. Stop services
make test-down
```

---

## No K8s Required

All tests use **mock data** from `mocks/k8fy_events.go`. No Kubernetes cluster needed:

✓ Fast (seconds, not minutes)  
✓ Deterministic (same data every time)  
✓ Isolated (no side effects)  
✓ Works offline  

When ready to test with real K8s, you can:
1. Run the K8fy adapter against a real cluster
2. Switch to `tests/integration/k8s_integration_test.go` (future)

---

## Troubleshooting

**Services won't start:**
```bash
# Check Docker
docker ps

# View logs
make test-logs

# Force clean restart
make test-clean && make test-up
```

**Tests timeout:**
- Increase timeout in `tests/integration/*_test.go`
- Ensure services are healthy: `make test-logs`

**Port conflicts:**
- Tests use non-standard ports (5433, 6380, 8081, 8000)
- If conflicts, edit `docker-compose.test.yml`

---

## Coverage

Generate a coverage report:
```bash
make test-coverage

# Opens coverage.html in browser showing which code is tested
```

---

## Next: Real K8s Testing

Once ready, test against a real cluster:

1. Install KinD (local k8s) or use existing cluster
2. Deploy K8fy adapter to the cluster
3. Run E2E tests that verify end-to-end flow
4. Add `tests/integration/k8s_integration_test.go`

For now, mock data is sufficient for rapid development.
