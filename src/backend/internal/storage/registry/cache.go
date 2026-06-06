package registry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/chan/agentify/backend/internal/models"
	"github.com/chan/agentify/backend/internal/telemetry"
)

// PodStore is the registry surface used by the orchestrator/handlers. Both the
// DynamoDB/in-memory *Registry and the *Cache wrapper implement it, so callers
// depend on the interface, not a concrete store.
type PodStore interface {
	UpsertPod(ctx context.Context, pod *models.Pod) error
	GetPod(ctx context.Context, podID string) (*models.Pod, error)
	ListPods(ctx context.Context, filter *models.PodFilter) ([]*models.Pod, error)
	ListPodsByNamespace(ctx context.Context, namespace string) ([]*models.Pod, error)
	ListActivePods(ctx context.Context) ([]*models.Pod, error)
	DeletePod(ctx context.Context, podID string) error
	UpdateQueryStats(ctx context.Context, podID string, hit bool, latencyMs int64) error
	UpdateFreshness(ctx context.Context, podID string, eventCount int64) error
	GetStats(ctx context.Context) (*models.PodStats, error)
}

var (
	_ PodStore = (*Registry)(nil)
	_ PodStore = (*Cache)(nil)
)

// Cache is a read-through snapshot cache over a PodStore (ADR 0012). It caches the
// whole (small, single-tenant) pod set, refreshes on TTL, serves stale on a refresh
// error up to maxStale, and invalidates only on set-changing writes.
type Cache struct {
	inner    PodStore
	ttl      time.Duration
	maxStale time.Duration
	logger   *slog.Logger

	mu       sync.RWMutex
	snapshot map[string]*models.Pod
	loadedAt time.Time
	valid    bool
}

// NewCache wraps a PodStore with a snapshot cache.
func NewCache(inner PodStore, ttl, maxStale time.Duration, logger *slog.Logger) *Cache {
	return &Cache{inner: inner, ttl: ttl, maxStale: maxStale, logger: logger}
}

// pods returns the current pod set: a cached copy if fresh, a refreshed set if
// stale, or the last good set if a refresh fails (up to maxStale).
func (c *Cache) pods(ctx context.Context) ([]*models.Pod, error) {
	c.mu.RLock()
	valid, age, snap := c.valid, time.Since(c.loadedAt), c.snapshot
	c.mu.RUnlock()

	if valid && age < c.ttl {
		telemetry.RegistryCacheTotal.WithLabelValues("hit").Inc()
		return clonePods(snap), nil
	}

	fresh, err := c.inner.ListPods(ctx, nil)
	if err != nil {
		if valid && age < c.maxStale {
			telemetry.RegistryCacheTotal.WithLabelValues("stale").Inc()
			c.logger.Warn("registry refresh failed; serving stale snapshot",
				"age_ms", age.Milliseconds(), "error", err)
			return clonePods(snap), nil
		}
		telemetry.RegistryCacheTotal.WithLabelValues("error").Inc()
		return nil, err
	}

	m := make(map[string]*models.Pod, len(fresh))
	for _, p := range fresh {
		m[p.ID] = p
	}
	c.mu.Lock()
	c.snapshot, c.loadedAt, c.valid = m, time.Now(), true
	c.mu.Unlock()
	telemetry.RegistryCacheTotal.WithLabelValues("miss").Inc()
	return clonePods(m), nil
}

func (c *Cache) GetPod(ctx context.Context, podID string) (*models.Pod, error) {
	pods, err := c.pods(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pods {
		if p.ID == podID {
			return p, nil
		}
	}
	return nil, fmt.Errorf("pod not found: %s", podID)
}

func (c *Cache) ListPods(ctx context.Context, filter *models.PodFilter) ([]*models.Pod, error) {
	pods, err := c.pods(ctx)
	if err != nil {
		return nil, err
	}
	if filter != nil {
		pods = filterPods(pods, filter)
	}
	return pods, nil
}

func (c *Cache) ListPodsByNamespace(ctx context.Context, namespace string) ([]*models.Pod, error) {
	return c.ListPods(ctx, &models.PodFilter{Namespace: &namespace})
}

func (c *Cache) ListActivePods(ctx context.Context) ([]*models.Pod, error) {
	active := "active"
	return c.ListPods(ctx, &models.PodFilter{Lifecycle: &active})
}

func (c *Cache) UpsertPod(ctx context.Context, pod *models.Pod) error {
	err := c.inner.UpsertPod(ctx, pod)
	if err == nil {
		c.invalidate() // a newly-formed pod/shard must be visible next read
	}
	return err
}

func (c *Cache) DeletePod(ctx context.Context, podID string) error {
	err := c.inner.DeletePod(ctx, podID)
	if err == nil {
		c.invalidate()
	}
	return err
}

// UpdateFreshness/UpdateQueryStats don't change the pod set — pass through without
// invalidating, so frequent per-ingest updates keep the cache warm (ADR 0012).
func (c *Cache) UpdateFreshness(ctx context.Context, podID string, eventCount int64) error {
	return c.inner.UpdateFreshness(ctx, podID, eventCount)
}

func (c *Cache) UpdateQueryStats(ctx context.Context, podID string, hit bool, latencyMs int64) error {
	return c.inner.UpdateQueryStats(ctx, podID, hit, latencyMs)
}

// GetStats is admin/infrequent — delegate (may lag the cached set; acceptable).
func (c *Cache) GetStats(ctx context.Context) (*models.PodStats, error) {
	return c.inner.GetStats(ctx)
}

func (c *Cache) invalidate() {
	c.mu.Lock()
	c.valid = false
	c.mu.Unlock()
}

// clonePods returns copies so callers can't mutate the cached snapshot.
func clonePods(m map[string]*models.Pod) []*models.Pod {
	out := make([]*models.Pod, 0, len(m))
	for _, p := range m {
		cp := *p
		out = append(out, &cp)
	}
	return out
}
