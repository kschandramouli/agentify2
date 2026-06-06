#!/bin/bash

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}Agentify Integration Test Runner${NC}"
echo -e "${YELLOW}========================================${NC}"

# Check if docker-compose is available
if ! command -v docker-compose &> /dev/null; then
    echo -e "${RED}Error: docker-compose is required but not installed${NC}"
    exit 1
fi

# Change to tests directory
cd "$(dirname "$0")"

# Start services
echo -e "\n${YELLOW}[1/3] Starting test services...${NC}"
docker-compose -f docker-compose.test.yml down 2>/dev/null || true
docker-compose -f docker-compose.test.yml up -d

# Wait for services to be healthy
echo -e "${YELLOW}[2/3] Waiting for services to be healthy...${NC}"
for i in {1..30}; do
    if docker-compose -f docker-compose.test.yml exec -T postgres pg_isready -U postgres &>/dev/null && \
       docker-compose -f docker-compose.test.yml exec -T redis redis-cli ping &>/dev/null && \
       docker-compose -f docker-compose.test.yml exec -T weaviate curl -s http://localhost:8080/v1/.well-known/ready &>/dev/null; then
        echo -e "${GREEN}✓ All services are healthy${NC}"
        break
    fi
    if [ $i -eq 30 ]; then
        echo -e "${RED}Timeout waiting for services${NC}"
        docker-compose -f docker-compose.test.yml logs
        exit 1
    fi
    echo -n "."
    sleep 1
done

# Run tests
echo -e "\n${YELLOW}[3/3] Running integration tests...${NC}"
# Tests live inside the backend Go module (so they can import internal packages).
cd ../src/backend
export POSTGRES_URL="postgres://postgres:postgres@localhost:5433/agentify_test"
export REDIS_URL="localhost:6380"
export WEAVIATE_URL="http://localhost:8081"

if go test -v ./tests/integration/...; then
    echo -e "\n${GREEN}========================================${NC}"
    echo -e "${GREEN}✓ All tests passed!${NC}"
    echo -e "${GREEN}========================================${NC}"

    # Optional: keep services running
    echo -e "\n${YELLOW}Services still running. To stop:${NC}"
    echo "  docker-compose -f tests/docker-compose.test.yml down"

    exit 0
else
    echo -e "\n${RED}========================================${NC}"
    echo -e "${RED}✗ Tests failed${NC}"
    echo -e "${RED}========================================${NC}"

    # Show logs for debugging
    echo -e "\n${YELLOW}Service logs:${NC}"
    docker-compose -f tests/docker-compose.test.yml logs

    docker-compose -f tests/docker-compose.test.yml down
    exit 1
fi
