# Docker image for the K8fy adapter (Python).
FROM python:3.11-slim

WORKDIR /app

# Install dependencies first for better layer caching.
COPY src/adapters/k8fy/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

# Copy the adapter package, preserving the package path so relative imports work.
COPY src/adapters/k8fy/ ./k8fy/

ENV K8S_NAMESPACE=default \
    SCRAPE_INTERVAL=30 \
    CERT_CHECK_INTERVAL=300 \
    BACKEND_URL=http://backend:8080

# Run as a module so the package-relative imports (from . import ...) resolve.
CMD ["python", "-m", "k8fy.main"]
