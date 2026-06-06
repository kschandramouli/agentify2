.PHONY: test test-up test-down test-logs test-clean test-integration

# Start test services (Docker Compose)
test-up:
	docker-compose -f tests/docker-compose.test.yml up -d
	@echo "Waiting for services to be healthy..."
	@sleep 5
	@docker-compose -f tests/docker-compose.test.yml ps

# Stop test services
test-down:
	docker-compose -f tests/docker-compose.test.yml down

# View test service logs
test-logs:
	docker-compose -f tests/docker-compose.test.yml logs -f

# Clean up test data and services
test-clean:
	docker-compose -f tests/docker-compose.test.yml down -v
	rm -rf tests/testdata/results

# Run integration tests (requires test-up first)
test-integration: test-up
	@echo "Running integration tests..."
	cd src/backend && go test -v ./tests/integration/...
	@echo "✓ Integration tests passed"

# Full test suite (start services, run tests, stop services)
test: test-clean test-up test-integration test-down
	@echo "✓ All tests completed"

# Run unit tests only
test-unit:
	cd src/backend && go test -v ./...

# Run tests with coverage
test-coverage: test-up
	cd src/backend && go test -v -coverprofile=coverage.out ./tests/integration/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Quick test script
test-quick:
	bash tests/run.sh
