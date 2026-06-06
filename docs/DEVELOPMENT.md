# Local Development Setup

## Prerequisites

- Docker & Docker Compose
- Go 1.21+
- Python 3.11+
- PostgreSQL (or use Docker)
- Redis (or use Docker)

## Quick start

### 1. Clone and setup

```bash
git clone ...
cd agentify
cp .env.example .env
```

### 2. Start local services (Docker Compose)

```bash
docker-compose up -d
```

This starts:
- **Weaviate** (vector DB) on `http://localhost:8080`

For other services (Postgres, Redis), add them to `docker-compose.yml` or use local installations.

### 3. Run backend (Go)

```bash
cd src/backend
go mod download
go run cmd/agentify/main.go
```

Backend runs on `http://localhost:8080`

### 4. Run agent service (Python)

```bash
cd src/agent
python -m pip install -r requirements.txt
export ANTHROPIC_API_KEY=your-key-here
python main.py
```

Agent runs on `http://localhost:8001`

### 5. Test the system

```bash
# Health check
curl http://localhost:8080/health

# Query endpoint (placeholder)
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"question": "Is the payments service healthy?"}'

# Agent health
curl http://localhost:8001/health
```

---

## Weaviate: local vs. cloud

### Local (current setup)

```bash
# Start Weaviate via docker-compose
docker-compose up weaviate

# Check it's running
curl http://localhost:8080/v1/.well-known/live
```

Weaviate stores vectors locally in `weaviate_data/` volume.

### Enterprise (EKS)

```bash
# Deploy to EKS cluster
kubectl apply -f infra/kubernetes/weaviate.yaml

# Check deployment
kubectl get pods -n agentify
kubectl logs -n agentify -l app=weaviate -f

# Port-forward (if needed)
kubectl port-forward -n agentify svc/weaviate 8080:8080
```

Update backend config:
```
VECTOR_STORE_TYPE=weaviate
VECTOR_STORE_ENDPOINT=weaviate.agentify.svc.cluster.local:8080
```

---

## Stopping services

```bash
# Stop Docker services
docker-compose down

# Or stop just Weaviate
docker-compose down weaviate
```

---

## Troubleshooting

**Weaviate won't start:**
- Check Docker is running: `docker ps`
- Check logs: `docker-compose logs weaviate`
- Try removing old volumes: `docker-compose down -v`

**Backend can't reach Weaviate:**
- Ensure Docker service is running: `docker-compose ps`
- Check endpoint in `.env`: `VECTOR_STORE_ENDPOINT=localhost:8080`
- Try: `curl http://localhost:8080/v1/.well-known/live`

**Port conflicts:**
- Weaviate: 8080
- Backend: 8080 (change `PORT` in `.env`)
- Agent: 8001
