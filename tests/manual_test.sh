#!/bin/bash

# Manual testing with curl and test data

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

API_URL="${API_URL:-http://localhost:8080}"

echo -e "${YELLOW}Manual Testing Guide${NC}"
echo "API_URL: $API_URL"
echo ""

# 1. Health check
echo -e "${YELLOW}1. Health check:${NC}"
curl -s "$API_URL/health" | jq .
echo ""

# 2. List pods
echo -e "${YELLOW}2. List pods in registry:${NC}"
curl -s "$API_URL/admin/pods" | jq .
echo ""

# 3. Ingest a pod restart event
echo -e "${YELLOW}3. Ingest pod restart event:${NC}"
curl -s -X POST "$API_URL/api/ingest" \
  -H "Content-Type: application/json" \
  -d @testdata/pod_restart_event.json | jq .
echo ""

# 4. Ingest a certificate event
echo -e "${YELLOW}4. Ingest certificate event:${NC}"
curl -s -X POST "$API_URL/api/ingest" \
  -H "Content-Type: application/json" \
  -d @testdata/certificate_event.json | jq .
echo ""

# 5. Query for health (placeholder)
echo -e "${YELLOW}5. Query health (not yet implemented):${NC}"
curl -s -X POST "$API_URL/api/query" \
  -H "Content-Type: application/json" \
  -d '{"question": "Is payment service healthy?", "context": {"namespace": "prod"}}' | jq .
echo ""

echo -e "${GREEN}✓ Manual testing complete${NC}"
