package registry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/chan/agentify/backend/internal/models"
)

// Registry manages the pod registry. In production it is backed by DynamoDB;
// when constructed with a nil client it runs fully in-memory, which is used for
// local development and tests (no AWS required). The registry is the
// orchestrator's source of truth for what pods exist and their current state.
type Registry struct {
	client    *dynamodb.Client
	tableName string
	logger    *slog.Logger

	// In-memory store, used only when client == nil.
	mu  sync.RWMutex
	mem map[string]*models.Pod
}

// NewRegistry creates a new pod registry. Pass a real DynamoDB client for
// production; pass nil to run in-memory (local dev / tests).
func NewRegistry(client *dynamodb.Client, tableName string, logger *slog.Logger) *Registry {
	r := &Registry{
		client:    client,
		tableName: tableName,
		logger:    logger,
	}
	if client == nil {
		r.mem = make(map[string]*models.Pod)
		logger.Info("pod registry running in-memory (no DynamoDB client)")
	}
	return r
}

// inMemory reports whether the registry is using the in-memory store.
func (r *Registry) inMemory() bool { return r.client == nil }

// UpsertPod creates or updates a pod in the registry.
func (r *Registry) UpsertPod(ctx context.Context, pod *models.Pod) error {
	pod.UpdatedAt = time.Now()
	if pod.CreatedAt.IsZero() {
		pod.CreatedAt = time.Now()
	}

	if r.inMemory() {
		r.mu.Lock()
		stored := *pod // copy so later caller mutations don't alias the stored value
		r.mem[pod.ID] = &stored
		r.mu.Unlock()
		r.logger.Info("pod upserted (memory)", "pod_id", pod.ID, "store_type", pod.StoreType)
		return nil
	}

	av, err := attributevalue.MarshalMap(pod)
	if err != nil {
		return fmt.Errorf("failed to marshal pod: %w", err)
	}

	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName),
		Item:      av,
	})
	if err != nil {
		r.logger.Error("failed to upsert pod", "pod_id", pod.ID, "error", err)
		return err
	}

	r.logger.Info("pod upserted", "pod_id", pod.ID, "store_type", pod.StoreType)
	return nil
}

// GetPod retrieves a pod by ID.
func (r *Registry) GetPod(ctx context.Context, podID string) (*models.Pod, error) {
	if r.inMemory() {
		r.mu.RLock()
		pod, ok := r.mem[podID]
		r.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("pod not found: %s", podID)
		}
		copy := *pod
		return &copy, nil
	}

	resp, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"id": &types.AttributeValueMemberS{Value: podID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	if len(resp.Item) == 0 {
		return nil, fmt.Errorf("pod not found: %s", podID)
	}

	var pod models.Pod
	err = attributevalue.UnmarshalMap(resp.Item, &pod)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal pod: %w", err)
	}

	return &pod, nil
}

// ListPods returns all pods matching the filter.
// If filter is nil, returns all pods.
func (r *Registry) ListPods(ctx context.Context, filter *models.PodFilter) ([]*models.Pod, error) {
	if r.inMemory() {
		r.mu.RLock()
		pods := make([]*models.Pod, 0, len(r.mem))
		for _, p := range r.mem {
			copy := *p
			pods = append(pods, &copy)
		}
		r.mu.RUnlock()
		if filter != nil {
			pods = filterPods(pods, filter)
		}
		return pods, nil
	}

	// TODO: implement filtering with DynamoDB query/scan + FilterExpression
	// For MVP, scan all and filter in memory
	resp, err := r.client.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(r.tableName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan pods: %w", err)
	}

	var pods []*models.Pod
	err = attributevalue.UnmarshalListOfMaps(resp.Items, &pods)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal pods: %w", err)
	}

	// Apply filter in memory
	if filter != nil {
		pods = filterPods(pods, filter)
	}

	return pods, nil
}

// ListPodsByNamespace returns all pods in a namespace.
func (r *Registry) ListPodsByNamespace(ctx context.Context, namespace string) ([]*models.Pod, error) {
	return r.ListPods(ctx, &models.PodFilter{Namespace: &namespace})
}

// ListActivePods returns all pods in "active" lifecycle.
func (r *Registry) ListActivePods(ctx context.Context) ([]*models.Pod, error) {
	active := "active"
	return r.ListPods(ctx, &models.PodFilter{Lifecycle: &active})
}

// DeletePod removes a pod from the registry.
// Note: this should only be called for retired pods; active pods should transition
// through "draining" lifecycle first.
func (r *Registry) DeletePod(ctx context.Context, podID string) error {
	if r.inMemory() {
		r.mu.Lock()
		delete(r.mem, podID)
		r.mu.Unlock()
		r.logger.Info("pod deleted (memory)", "pod_id", podID)
		return nil
	}

	_, err := r.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"id": &types.AttributeValueMemberS{Value: podID},
		},
	})
	if err != nil {
		r.logger.Error("failed to delete pod", "pod_id", podID, "error", err)
		return err
	}

	r.logger.Info("pod deleted", "pod_id", podID)
	return nil
}

// UpdateQueryStats increments query counters for a pod.
// Called after each query to track hit/miss rates and latency.
func (r *Registry) UpdateQueryStats(ctx context.Context, podID string, hit bool, latencyMs int64) error {
	pod, err := r.GetPod(ctx, podID)
	if err != nil {
		return err
	}

	// Update stats
	pod.QueryStats.TotalQueries++
	pod.QueryStats.LastUpdated = time.Now()
	if hit {
		pod.QueryStats.Hits++
	} else {
		pod.QueryStats.Misses++
	}
	pod.QueryStats.P95LatencyMs = latencyMs // TODO: compute proper p95

	pod.Freshness = time.Now()
	return r.UpsertPod(ctx, pod)
}

// UpdateFreshness updates a pod's freshness timestamp.
// Called when new data is ingested into the pod.
func (r *Registry) UpdateFreshness(ctx context.Context, podID string, eventCount int64) error {
	pod, err := r.GetPod(ctx, podID)
	if err != nil {
		return err
	}

	pod.Freshness = time.Now()
	pod.EventCount = eventCount
	return r.UpsertPod(ctx, pod)
}

// GetStats aggregates statistics across pods.
func (r *Registry) GetStats(ctx context.Context) (*models.PodStats, error) {
	pods, err := r.ListActivePods(ctx)
	if err != nil {
		return nil, err
	}

	stats := &models.PodStats{
		ByStoreType: make(map[string]int),
		ByLifecycle: make(map[string]int),
	}

	var totalMissRate float64
	for _, pod := range pods {
		stats.TotalPods++
		stats.TotalEvents += pod.EventCount
		stats.ByStoreType[pod.StoreType]++
		stats.ByLifecycle[pod.Lifecycle]++
		totalMissRate += pod.QueryStats.MissRate()
	}

	if stats.TotalPods > 0 {
		stats.AverageMissRate = totalMissRate / float64(stats.TotalPods)
	}

	return stats, nil
}

// filterPods applies the filter to a list of pods.
func filterPods(pods []*models.Pod, filter *models.PodFilter) []*models.Pod {
	var result []*models.Pod
	for _, pod := range pods {
		if filter.Namespace != nil && pod.Namespace != *filter.Namespace {
			continue
		}
		if filter.StoreType != nil && pod.StoreType != *filter.StoreType {
			continue
		}
		if filter.Lifecycle != nil && pod.Lifecycle != *filter.Lifecycle {
			continue
		}
		if len(filter.Tags) > 0 && !hasAnyTag(pod.Tags, filter.Tags) {
			continue
		}
		result = append(result, pod)
	}
	return result
}

// hasAnyTag checks if a pod has any of the given tags.
func hasAnyTag(podTags []string, filterTags []string) bool {
	for _, filterTag := range filterTags {
		for _, podTag := range podTags {
			if podTag == filterTag {
				return true
			}
		}
	}
	return false
}
