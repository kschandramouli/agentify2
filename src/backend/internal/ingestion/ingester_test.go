package ingestion

import (
	"context"
	"log/slog"
	"io"
	"testing"
	"time"

	"github.com/chan/agentify/backend/internal/models"
	"github.com/chan/agentify/backend/internal/storage"
	"github.com/chan/agentify/backend/internal/storage/registry"
)

// fakeBackend is a minimal in-memory storage.Backend — records every Store
// call's podID so tests can assert on cluster-aware pod-ID construction
// (ADR 0024) without needing a real Postgres instance.
type fakeBackend struct {
	stored []storedCall
}

type storedCall struct {
	podID string
	data  map[string]interface{}
}

func (f *fakeBackend) Store(ctx context.Context, podID string, data map[string]interface{}) (string, error) {
	f.stored = append(f.stored, storedCall{podID: podID, data: data})
	return podID, nil
}
func (f *fakeBackend) Query(ctx context.Context, podID string, query map[string]interface{}) ([]map[string]interface{}, error) {
	return nil, nil
}
func (f *fakeBackend) HealthCheck(ctx context.Context) error { return nil }
func (f *fakeBackend) Close() error                          { return nil }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func liveStateEvent(namespace, podID string) *models.Event {
	return &models.Event{
		EventNamespace: "k8fy.live-state",
		Type:           "pod_modified",
		Source:         "kubernetes-api",
		EntityKey:      podID,
		Payload:        map[string]interface{}{"namespace": namespace, "pod_id": podID},
		Traits:         models.EventTraits{Authority: "derived"},
	}
}

// TestIngestClusterScopedPodIDs is the core regression test for ADR 0024:
// two clusters both reporting a "payments" namespace must land in different
// pods, not collide in the same one.
func TestIngestClusterScopedPodIDs(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	reg := registry.NewRegistry(nil, "test-table", logger)
	kv := &fakeBackend{}
	bf := storage.NewBackendFactory(nil, kv, nil)
	ing := NewIngester(reg, bf, logger)

	t.Run("empty clusterID reproduces today's unscoped pod ID", func(t *testing.T) {
		result, err := ing.Ingest(ctx, liveStateEvent("payments", "payment-api-abc"), "tenant-a", "")
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if result.PodID != "k8fy.live-state.payments" {
			t.Errorf("PodID = %q, want %q", result.PodID, "k8fy.live-state.payments")
		}
	})

	t.Run("two clusters reporting the same namespace land in different pods", func(t *testing.T) {
		resultA, err := ing.Ingest(ctx, liveStateEvent("payments", "payment-api-abc"), "tenant-a", "cluster-a")
		if err != nil {
			t.Fatalf("ingest cluster-a: %v", err)
		}
		resultB, err := ing.Ingest(ctx, liveStateEvent("payments", "payment-api-abc"), "tenant-a", "cluster-b")
		if err != nil {
			t.Fatalf("ingest cluster-b: %v", err)
		}
		if resultA.PodID == resultB.PodID {
			t.Fatalf("cluster-a and cluster-b collided in the same pod: %q", resultA.PodID)
		}
		if resultA.PodID != "k8fy.live-state.cluster-a.payments" {
			t.Errorf("cluster-a PodID = %q, want %q", resultA.PodID, "k8fy.live-state.cluster-a.payments")
		}
		if resultB.PodID != "k8fy.live-state.cluster-b.payments" {
			t.Errorf("cluster-b PodID = %q, want %q", resultB.PodID, "k8fy.live-state.cluster-b.payments")
		}
	})

	t.Run("tenant_id and cluster_id are written into the stored data", func(t *testing.T) {
		kv.stored = nil
		if _, err := ing.Ingest(ctx, liveStateEvent("checkout", "checkout-api-xyz"), "tenant-z", "cluster-9"); err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if len(kv.stored) != 1 {
			t.Fatalf("expected 1 store call, got %d", len(kv.stored))
		}
		call := kv.stored[0]
		if call.data["tenant_id"] != "tenant-z" {
			t.Errorf("tenant_id = %v, want tenant-z", call.data["tenant_id"])
		}
		if call.data["cluster_id"] != "cluster-9" {
			t.Errorf("cluster_id = %v, want cluster-9", call.data["cluster_id"])
		}
	})
}

// TestIngestUnshardedProfileClusterScoping covers the single-global-pod
// families (certificates/events/metrics — profile.Sharded == false), which
// collided across every namespace AND every cluster before ADR 0024.
func TestIngestUnshardedProfileClusterScoping(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	reg := registry.NewRegistry(nil, "test-table", logger)
	kv := &fakeBackend{} // k8fy.certificates is StoreType "kv" (pod_profiles.go)
	bf := storage.NewBackendFactory(nil, kv, nil)
	ing := NewIngester(reg, bf, logger)

	certEvent := func() *models.Event {
		return &models.Event{
			ID:             "evt-" + time.Now().Format(time.RFC3339Nano),
			EventNamespace: "k8fy.certificates",
			Type:           "cert_status",
			Source:         "kubernetes-api",
			Payload:        map[string]interface{}{"namespace": "payments"},
			Traits:         models.EventTraits{Authority: "derived"},
		}
	}

	resultA, err := ing.Ingest(ctx, certEvent(), "tenant-a", "cluster-a")
	if err != nil {
		t.Fatalf("ingest cluster-a: %v", err)
	}
	resultB, err := ing.Ingest(ctx, certEvent(), "tenant-a", "cluster-b")
	if err != nil {
		t.Fatalf("ingest cluster-b: %v", err)
	}
	if resultA.PodID == resultB.PodID {
		t.Fatalf("certificate pods collided across clusters: %q", resultA.PodID)
	}
	if resultA.PodID != "k8fy.certificates.cluster-a" {
		t.Errorf("cluster-a cert PodID = %q, want %q", resultA.PodID, "k8fy.certificates.cluster-a")
	}

	resultNoCluster, err := ing.Ingest(ctx, certEvent(), "tenant-a", "")
	if err != nil {
		t.Fatalf("ingest no cluster: %v", err)
	}
	if resultNoCluster.PodID != "k8fy.certificates" {
		t.Errorf("unscoped cert PodID = %q, want %q (today's unchanged shape)", resultNoCluster.PodID, "k8fy.certificates")
	}
}
