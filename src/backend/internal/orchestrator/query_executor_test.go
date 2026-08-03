package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/chan/agentify/backend/internal/models"
	"github.com/chan/agentify/backend/internal/storage"
	"github.com/chan/agentify/backend/internal/storage/registry"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRouteToPodsClusterScoping is the read-side counterpart to
// ingestion's TestIngestClusterScopedPodIDs (ADR 0024): given the same
// cluster-aware pod IDs ingestion produces, RouteToPods must resolve the
// matching cluster's shard, not a different cluster's (or an unscoped one).
func TestRouteToPodsClusterScoping(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	reg := registry.NewRegistry(nil, "test-table", logger)
	bf := storage.NewBackendFactory(nil, nil, nil)
	qe := NewQueryExecutor(reg, bf, logger)

	seed := func(id string) {
		if err := reg.UpsertPod(ctx, &models.Pod{ID: id, Kind: "leaf", Namespace: "k8fy", StoreType: "kv"}); err != nil {
			t.Fatalf("seed pod %s: %v", id, err)
		}
	}
	seed("k8fy.live-state.payments")
	seed("k8fy.live-state.cluster-a.payments")
	seed("k8fy.live-state.cluster-b.payments")
	seed("k8fy.certificates")
	seed("k8fy.certificates.cluster-a")
	seed("k8fy.metrics.cluster-a")
	seed("k8fy.events.cluster-a")

	t.Run("health_check: empty clusterID routes to the unscoped shard", func(t *testing.T) {
		pods, err := qe.RouteToPods(ctx, "health_check", "payments", "")
		if err != nil || len(pods) != 1 || pods[0].ID != "k8fy.live-state.payments" {
			t.Fatalf("pods=%v err=%v", pods, err)
		}
	})

	t.Run("health_check: cluster-a and cluster-b resolve to different shards", func(t *testing.T) {
		podsA, err := qe.RouteToPods(ctx, "health_check", "payments", "cluster-a")
		if err != nil || len(podsA) != 1 || podsA[0].ID != "k8fy.live-state.cluster-a.payments" {
			t.Fatalf("cluster-a: pods=%v err=%v", podsA, err)
		}
		podsB, err := qe.RouteToPods(ctx, "health_check", "payments", "cluster-b")
		if err != nil || len(podsB) != 1 || podsB[0].ID != "k8fy.live-state.cluster-b.payments" {
			t.Fatalf("cluster-b: pods=%v err=%v", podsB, err)
		}
	})

	t.Run("cert_check: cluster-scoped vs. unscoped are different pods", func(t *testing.T) {
		unscoped, err := qe.RouteToPods(ctx, "cert_check", "", "")
		if err != nil || len(unscoped) != 1 || unscoped[0].ID != "k8fy.certificates" {
			t.Fatalf("unscoped: pods=%v err=%v", unscoped, err)
		}
		scoped, err := qe.RouteToPods(ctx, "cert_check", "", "cluster-a")
		if err != nil || len(scoped) != 1 || scoped[0].ID != "k8fy.certificates.cluster-a" {
			t.Fatalf("scoped: pods=%v err=%v", scoped, err)
		}
	})

	t.Run("metrics_history and change_history route to cluster-scoped single-global pods", func(t *testing.T) {
		metrics, err := qe.RouteToPods(ctx, "metrics_history", "", "cluster-a")
		if err != nil || len(metrics) != 1 || metrics[0].ID != "k8fy.metrics.cluster-a" {
			t.Fatalf("metrics: pods=%v err=%v", metrics, err)
		}
		changes, err := qe.RouteToPods(ctx, "change_history", "", "cluster-a")
		if err != nil || len(changes) != 1 || changes[0].ID != "k8fy.events.cluster-a" {
			t.Fatalf("changes: pods=%v err=%v", changes, err)
		}
	})

	t.Run("unknown cluster degrades to no pods, not an error", func(t *testing.T) {
		pods, err := qe.RouteToPods(ctx, "cert_check", "", "cluster-nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pods) != 0 {
			t.Errorf("want no pods for an unregistered cluster, got %v", pods)
		}
	})
}
