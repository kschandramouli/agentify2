# Local Development & Testing

> Rewritten 2026-08-04 — the previous version listed Redis as a
> prerequisite and Weaviate as the docker-compose quick-start step; both
> were removed from the runtime in [ADR 0010](../context-mesh/decisions/0010-postgres-single-store.md)
> (2026-06-02), a Postgres-only store. This version matches what actually
> runs today, and absorbs the former standalone `TESTING.md`.

## Prerequisites

- Go 1.24+
- Python 3.11+
- PostgreSQL (Docker, a local install, or none at all — see "Postgres for
  local dev" below; there's no hard requirement to have one running just to
  start the backend)
- Docker (only needed for the integration test stack, not for basic dev)

Redis and Weaviate are **not** required — both were removed from the
runtime path. Weaviate stays in this repo only as documented-inert config
(`VECTOR_STORE_TYPE=weaviate`) for a future vector-store re-adoption; no
current feature needs it running.

## Quick start

```bash
git clone ...
cd agentify
cp .env.example .env
```

### Run the backend (Go)

```bash
cd src/backend
go mod download
REGISTRY_BACKEND=memory go run cmd/agentify/main.go
```

`REGISTRY_BACKEND=memory` runs the pod registry in-process (no DynamoDB/AWS
needed). Without a `DB_HOST` pointing at a running Postgres, relational/kv
queries will fail health checks but the server still starts — see
"Postgres for local dev" below to wire one up.

Backend runs on `http://localhost:8080`.

### Run the agent service (Python)

```bash
cd src/agent
python -m pip install -r requirements.txt
export ANTHROPIC_API_KEY=your-key-here
python main.py
```

Agent runs on `http://localhost:8001`.

### Smoke test

```bash
curl http://localhost:8080/health
curl http://localhost:8001/health

curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"question": "Is the payments service healthy?"}'
```

## Postgres for local dev

Two options, both already used elsewhere in this repo:

- **`embedded-postgres`** — what every Go unit/integration test that touches
  Postgres actually uses (`src/backend/internal/storage/postgres/postgres_test.go`'s
  `startEmbedded` helper): downloads and runs a real Postgres binary
  in-process on `localhost:54329`, no Docker needed. Not wired into
  `cmd/agentify/main.go` itself, but the same package is trivially reusable
  if you want a zero-Docker way to run the backend against a real Postgres
  locally.
- **Docker** — `docker run -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:15-alpine`,
  then set `DB_HOST=localhost DB_PORT=5432 DB_USER=postgres
  DB_PASSWORD=postgres DB_NAME=agentify` before starting the backend.

## Running the fleet collector locally (optional)

`agentify-discovery` (`src/adapters/discovery/`) and the original K8fy
adapter (`src/adapters/k8fy/`) both need a real or fake Kubernetes API to do
anything useful — not part of the basic backend+agent quick start. See
[K8FY_ADAPTER.md](K8FY_ADAPTER.md) and [AGENT_INTEGRATION.md](AGENT_INTEGRATION.md)'s
"Fleet clusters & live drill-down" section if you need to run either
locally.

---

## Testing

Three layers: **unit** (fast, in-memory, isolated logic), **integration**
(real Postgres via Docker, full flows), **manual** (curl examples).

### Run all tests

```bash
make test              # full integration suite: starts services, runs tests, stops services
make test-unit         # Go unit tests only, no Docker needed
```

### Run integration tests against a running stack

```bash
make test-up            # starts tests/docker-compose.test.yml
make test-integration   # cd src/backend && go test ./tests/integration/...
make test-down
make test-logs          # tail the test-stack container logs
```

`tests/docker-compose.test.yml` currently also defines `redis`, `weaviate`,
and `dynamodb-local` services alongside `postgres` — **only `postgres`
(port `5433`) is actually used** by any current test
(`tests/integration/event_ingestion_test.go` connects to it directly); the
other three are vestigial leftovers from the pre-ADR-0010 polyglot stack
and pre-DynamoDB-emulator-removal era. Left in place rather than removed as
part of this doc pass — flagging here so you're not left wondering why
`make test-up` starts containers nothing talks to.

### Test structure

- **Mock data generators** (`tests/mocks/k8fy_events.go`) — generate fake
  K8fy events without touching Kubernetes:
  ```go
  gen := mocks.NewK8fyEventGenerator("prod")
  event := gen.GeneratePodRestartEvent("payment-svc-abc")
  event := gen.GeneratePodHealthyEvent("api-server-123")
  event := gen.GenerateCertificateExpiringEvent("tls-cert", 12)
  events := gen.GenerateBulkPodEvents("test-pod", 100)
  ```
- **Test fixtures** (`tests/fixtures/`) — seed the pod registry:
  ```go
  fixtures := fixtures.NewPodFixtures(registry, logger)
  fixtures.SeedK8fyPods(ctx)
  fixtures.ClearPods(ctx)
  ```
- **Integration tests** (`src/backend/tests/integration/`) — full flow
  tests against the real (Dockerized) Postgres from `make test-up`.
- **Manual testing** (`tests/manual_test.sh`) — real HTTP requests:
  ```bash
  cd src/backend && go run cmd/agentify/main.go   # terminal 1
  make test-up                                     # terminal 2
  bash tests/manual_test.sh                        # terminal 3
  ```

### Test data

Pre-made JSON events in `tests/testdata/` (`pod_restart_event.json`,
`certificate_event.json`):

```bash
curl -X POST http://localhost:8080/api/ingest \
  -H "Content-Type: application/json" \
  -d @tests/testdata/pod_restart_event.json
```

### Coverage

```bash
make test-coverage   # writes coverage.html
```

### No live Kubernetes required

Every test above uses mock data or the real (but Kubernetes-independent)
Postgres/registry stack — fast, deterministic, works offline. To validate
against a real cluster, run `k8fy-adapter` or `agentify-discovery` against
one directly (see "Running the fleet collector locally" above) rather than
through this test suite.

---

## Troubleshooting

**Backend can't reach Postgres:**
- Check `DB_HOST`/`DB_PORT` in `.env` match a running instance
- `REGISTRY_BACKEND=memory` avoids needing DynamoDB, but not Postgres —
  relational/kv queries still need `DB_HOST` set to something real

**Test services won't start:**
```bash
docker ps               # confirm Docker is running
make test-logs           # view container logs
make test-clean && make test-up   # force clean restart
```

**Tests time out:**
- Increase the timeout in the relevant `tests/integration/*_test.go`
- Confirm services are healthy: `make test-logs`

**Port conflicts:**
- Dev Postgres (if using Docker): `5432`
- Test-stack Postgres: `5433` (edit `tests/docker-compose.test.yml` if it collides)
- Backend: `8080` (change `PORT` in `.env`)
- Agent: `8001`
