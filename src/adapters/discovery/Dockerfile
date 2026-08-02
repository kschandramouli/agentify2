# Docker image for agentify-discovery (ADR 0022 / ROADMAP P18) — the
# deterministic, per-cluster fleet collector.
FROM python:3.11-slim

WORKDIR /app

# Install dependencies first for better layer caching.
COPY requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

# Copy the package, preserving the package path so relative imports work.
COPY . ./discovery/

ENV BACKEND_URL=http://agentify-backend:8080 \
    SCAN_INTERVAL_SECONDS=60 \
    MAX_PODS_PER_NAMESPACE=5 \
    LOG_TAIL_LINES=200 \
    NAMESPACE_EXCLUDE=kube-system,kube-public,kube-node-lease \
    HEALTH_PORT=8300

# Run as a module so the package-relative imports (from . import ...) resolve.
CMD ["python", "-m", "discovery.main"]
