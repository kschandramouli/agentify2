package models

import (
	"time"
)

// Pod represents a single data storage unit in the context-mesh.
// See context-mesh/policies/pod-formation.md and ADR 0002 (recursive pods).
type Pod struct {
	// Identification
	ID       string `dynamodbav:"id" json:"id"`                          // unique pod ID
	Kind     string `dynamodbav:"kind" json:"kind"`                      // "leaf" | "index"
	Summary  string `dynamodbav:"summary" json:"summary"`                // one-line description
	Tags     []string `dynamodbav:"tags" json:"tags"`                    // e.g., ["billing", "k8fy"]
	Namespace string `dynamodbav:"namespace" json:"namespace"`           // logical grouping (e.g., "k8fy", "crm")

	// Storage configuration
	StoreType string `dynamodbav:"store_type" json:"store_type"`         // "relational" | "kv" | "vector" | "timeseries" | "logs" | "passthrough"
	Authority string `dynamodbav:"authority" json:"authority"`           // "system-of-record" | "derived"
	SchemaRef string `dynamodbav:"schema_ref" json:"schema_ref"`         // pointer to schema definition

	// Hierarchy (for index pods)
	Kind_IsIndex bool `dynamodbav:"-" json:"_is_index"`                  // convenience flag
	PartitionKey string `dynamodbav:"partition_key" json:"partition_key"` // dimension for sharding (e.g., "namespace")
	Shards []ShardRef `dynamodbav:"shards" json:"shards"`                // child pods (for index pods only)

	// Lifecycle
	Lifecycle string `dynamodbav:"lifecycle" json:"lifecycle"`           // "active" | "merging" | "draining" | "retired"
	Freshness time.Time `dynamodbav:"freshness" json:"freshness"`        // last modified timestamp

	// Metrics
	EventCount int64 `dynamodbav:"event_count" json:"event_count"`       // number of events stored
	QueryStats QueryStats `dynamodbav:"query_stats" json:"query_stats"` // hit/miss/latency

	// Metadata
	CreatedAt time.Time `dynamodbav:"created_at" json:"created_at"`
	UpdatedAt time.Time `dynamodbav:"updated_at" json:"updated_at"`

	// Multi-tenancy (ADR 0024) — metadata only, not part of the DynamoDB key
	// (that stays the single ID string below). Cluster isolation is achieved
	// by PodID embedding ClusterID into the ID itself, not by these fields;
	// they exist for introspection (e.g. an admin listing which cluster a
	// pod belongs to), not for routing.
	TenantID  string `dynamodbav:"tenant_id,omitempty" json:"tenant_id,omitempty"`
	ClusterID string `dynamodbav:"cluster_id,omitempty" json:"cluster_id,omitempty"`
}

// ShardRef is a reference to a child pod (for index pods).
type ShardRef struct {
	ChildID    string `dynamodbav:"child_id" json:"child_id"`           // pod ID of child
	Partition  string `dynamodbav:"partition" json:"partition"`         // partition value (e.g., "namespace=prod")
	EventCount int64  `dynamodbav:"event_count" json:"event_count"`     // events in this shard
}

// QueryStats tracks query performance metrics for a pod.
type QueryStats struct {
	Hits            int64 `dynamodbav:"hits" json:"hits"`                         // successful queries
	Misses          int64 `dynamodbav:"misses" json:"misses"`                     // queries that got no results
	P95LatencyMs    int64 `dynamodbav:"p95_latency_ms" json:"p95_latency_ms"`     // 95th percentile latency
	TotalQueries    int64 `dynamodbav:"total_queries" json:"total_queries"`
	LastUpdated     time.Time `dynamodbav:"last_updated" json:"last_updated"`
}

// MissRate returns the pod's miss rate (0.0 to 1.0).
func (qs QueryStats) MissRate() float64 {
	total := qs.Hits + qs.Misses
	if total == 0 {
		return 0
	}
	return float64(qs.Misses) / float64(total)
}

// PodFilter is used to query pods with specific criteria.
type PodFilter struct {
	Namespace  *string
	StoreType  *string
	Tags       []string
	Lifecycle  *string
}

// PodStats aggregates statistics across pods.
type PodStats struct {
	TotalPods      int
	ByStoreType    map[string]int
	ByLifecycle    map[string]int
	TotalEvents    int64
	AverageMissRate float64
}
