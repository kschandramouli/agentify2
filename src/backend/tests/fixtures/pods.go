package fixtures

import (
	"context"
	"log/slog"
	"time"

	"github.com/chan/agentify/backend/internal/models"
	"github.com/chan/agentify/backend/internal/storage/registry"
)

// PodFixtures seeds the pod registry with test data.
type PodFixtures struct {
	registry registry.PodStore
	logger   *slog.Logger
}

// NewPodFixtures creates a new fixture manager.
func NewPodFixtures(reg registry.PodStore, logger *slog.Logger) *PodFixtures {
	return &PodFixtures{
		registry: reg,
		logger:   logger,
	}
}

// SeedK8fyPods seeds the registry with K8fy pods.
func (pf *PodFixtures) SeedK8fyPods(ctx context.Context) error {
	pods := []*models.Pod{
		// Live-state index pod (sharded by namespace)
		{
			ID:        "k8fy.live-state",
			Kind:      "index",
			Summary:   "Live Kubernetes service/pod health, sharded by namespace",
			Namespace: "k8fy",
			Tags:      []string{"k8fy", "live", "health"},
			StoreType: "passthrough",
			Authority: "derived",
			Lifecycle: "active",
			Freshness: time.Now(),
		},
		// Live-state child shard for prod namespace
		{
			ID:        "k8fy.live-state.prod",
			Kind:      "leaf",
			Summary:   "Live pod/service health for prod namespace",
			Namespace: "k8fy",
			Tags:      []string{"k8fy", "prod", "live"},
			StoreType: "kv",
			Authority: "derived",
			Lifecycle: "active",
			Freshness: time.Now(),
		},
		// Live-state child shard for staging namespace
		{
			ID:        "k8fy.live-state.staging",
			Kind:      "leaf",
			Summary:   "Live pod/service health for staging namespace",
			Namespace: "k8fy",
			Tags:      []string{"k8fy", "staging", "live"},
			StoreType: "kv",
			Authority: "derived",
			Lifecycle: "active",
			Freshness: time.Now(),
		},
		// Events pod (append-only, time-series)
		{
			ID:        "k8fy.events",
			Kind:      "leaf",
			Summary:   "Kubernetes events and pod restart history",
			Namespace: "k8fy",
			Tags:      []string{"k8fy", "events", "logs"},
			StoreType: "relational",
			Authority: "derived",
			Lifecycle: "active",
			Freshness: time.Now(),
		},
		// Certificates pod
		{
			ID:        "k8fy.certificates",
			Kind:      "leaf",
			Summary:   "TLS certificate expiry tracking",
			Namespace: "k8fy",
			Tags:      []string{"k8fy", "certificates"},
			StoreType: "relational",
			Authority: "derived",
			Lifecycle: "active",
			Freshness: time.Now(),
		},
		// Metrics pod (time-series)
		{
			ID:        "k8fy.metrics",
			Kind:      "leaf",
			Summary:   "Pod CPU/memory metrics and trends",
			Namespace: "k8fy",
			Tags:      []string{"k8fy", "metrics", "timeseries"},
			StoreType: "timeseries",
			Authority: "derived",
			Lifecycle: "active",
			Freshness: time.Now(),
		},
	}

	for _, pod := range pods {
		if err := pf.registry.UpsertPod(ctx, pod); err != nil {
			pf.logger.Error("failed to seed pod", "pod_id", pod.ID, "error", err)
			return err
		}
	}

	pf.logger.Info("seeded k8fy pods", "count", len(pods))
	return nil
}

// SeedServiceData seeds live-state with service health data (for Redis).
// In real usage, this comes from adapters; for testing we seed directly.
func (pf *PodFixtures) SeedServiceData(ctx context.Context) error {
	// This would normally be populated via events
	// For now, just note that services are discovered via queries
	pf.logger.Info("service data will be populated via event ingestion")
	return nil
}

// ClearPods removes all test pods from the registry.
func (pf *PodFixtures) ClearPods(ctx context.Context) error {
	pods, err := pf.registry.ListActivePods(ctx)
	if err != nil {
		return err
	}

	for _, pod := range pods {
		if pod.Namespace == "k8fy" {
			if err := pf.registry.DeletePod(ctx, pod.ID); err != nil {
				pf.logger.Warn("failed to delete pod", "pod_id", pod.ID, "error", err)
			}
		}
	}

	pf.logger.Info("cleared test pods")
	return nil
}
