package integration

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/chan/agentify/backend/internal/ingestion"
	"github.com/chan/agentify/backend/internal/storage"
	"github.com/chan/agentify/backend/internal/storage/postgres"
	"github.com/chan/agentify/backend/internal/storage/registry"
	"github.com/chan/agentify/backend/tests/fixtures"
	"github.com/chan/agentify/backend/tests/mocks"
)

// connectTestBackends connects to the Postgres instance from
// docker-compose.test.yml and returns a backend factory. A single Postgres backs
// both store families (ADR 0010): relational (events) and kv (current-state).
// Skips the test if Postgres isn't reachable so runs without infra don't hard-fail.
func connectTestBackends(t *testing.T, logger *slog.Logger) *storage.BackendFactory {
	t.Helper()

	// Use a short context so the test skips quickly when Postgres isn't running
	// (e.g. in CI without the docker-compose.test.yml stack). A generous timeout
	// would block the test suite for minutes before the skip fires.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pgClient, err := postgres.NewClient(
		ctx,
		"postgres://postgres:postgres@localhost:5433/agentify_test?sslmode=disable",
		logger,
	)
	if err != nil {
		t.Skipf("postgres test backend unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pgClient.Close() })

	return storage.NewBackendFactory(pgClient, pgClient.CurrentStateStore(), nil)
}

// TestEventIngestion tests the full event ingestion flow.
func TestEventIngestion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	// Setup: connect to test storage backends
	backends := connectTestBackends(t, logger)

	// Create registry (would normally use DynamoDB)
	// For testing, use in-memory or mock
	reg := registry.NewRegistry(nil, "test-table", logger) // Mock for now

	// Seed test data
	fixtures := fixtures.NewPodFixtures(reg, logger)
	if err := fixtures.SeedK8fyPods(ctx); err != nil {
		t.Fatalf("failed to seed pods: %v", err)
	}

	// Create ingester
	ingester := ingestion.NewIngester(reg, backends, logger)

	// Generate test event
	gen := mocks.NewK8fyEventGenerator("prod")
	event := gen.GeneratePodRestartEvent("payment-svc-abc")

	// Test: Ingest the event
	result, err := ingester.Ingest(ctx, event, postgres.DefaultTenantID, "")
	if err != nil {
		t.Fatalf("failed to ingest event: %v", err)
	}

	// Verify
	if result.EventID != event.ID {
		t.Errorf("expected event_id %s, got %s", event.ID, result.EventID)
	}

	// The mock emits this event under "k8fy.live-state" with payload.namespace
	// "prod", so per the K8fy profile (ADR 0005) it lands in the per-namespace
	// live-state shard backed by KV.
	if result.PodID != "k8fy.live-state.prod" {
		t.Errorf("expected pod_id 'k8fy.live-state.prod', got %s", result.PodID)
	}

	if result.StoreType != "kv" {
		t.Errorf("expected store_type 'kv', got %s", result.StoreType)
	}

	t.Logf("✓ Event ingested: pod=%s, latency=%dms", result.PodID, result.Latency)
}

// TestPodCreation tests that new pods are created on first event.
func TestPodCreation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	reg := registry.NewRegistry(nil, "test-table", logger)
	ingester := ingestion.NewIngester(reg, connectTestBackends(t, logger), logger)

	gen := mocks.NewK8fyEventGenerator("prod")
	event := gen.GeneratePodRestartEvent("new-service-xyz")

	// First ingest should create the pod
	result1, err := ingester.Ingest(ctx, event, postgres.DefaultTenantID, "")
	if err != nil {
		t.Fatalf("failed to ingest: %v", err)
	}

	if !result1.CreatedPod {
		t.Error("expected created_pod=true on first ingest")
	}

	// Second ingest to same event type should reuse pod
	event2 := gen.GeneratePodHealthyEvent("another-pod")
	result2, err := ingester.Ingest(ctx, event2, postgres.DefaultTenantID, "")
	if err != nil {
		t.Fatalf("failed to ingest second event: %v", err)
	}

	if result1.PodID != result2.PodID {
		t.Errorf("expected same pod, got %s then %s", result1.PodID, result2.PodID)
	}

	if result2.CreatedPod {
		t.Error("expected created_pod=false on second ingest")
	}

	t.Log("✓ Pod creation works correctly")
}

// TestMultipleEvents tests ingesting multiple events.
func TestMultipleEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	reg := registry.NewRegistry(nil, "test-table", logger)
	ingester := ingestion.NewIngester(reg, connectTestBackends(t, logger), logger)

	gen := mocks.NewK8fyEventGenerator("prod")
	events := gen.GenerateBulkPodEvents("test-pod", 10)

	for i, event := range events {
		result, err := ingester.Ingest(ctx, event, postgres.DefaultTenantID, "")
		if err != nil {
			t.Fatalf("failed to ingest event %d: %v", i, err)
		}

		if result.EventID == "" {
			t.Errorf("event %d: empty event_id", i)
		}
	}

	t.Logf("✓ Ingested %d events successfully", len(events))
}
