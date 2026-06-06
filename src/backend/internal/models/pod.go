package models

import (
	"time"
)

// Pod represents a single data storage unit in the context-mesh.
// See context-mesh/policies/pod-formation.md and ADR 0002 (recursive pods).
type Pod struct {
	// Identification
	ID       string `dynamodb:"id" json:"id"`                          // unique pod ID
	Kind     string `dynamodb:"kind" json:"kind"`                      // "leaf" | "index"
	Summary  string `dynamodb:"summary" json:"summary"`                // one-line description
	Tags     []string `dynamodb:"tags" json:"tags"`                    // e.g., ["billing", "k8fy"]
	Namespace string `dynamodb:"namespace" json:"namespace"`           // logical grouping (e.g., "k8fy", "crm")

	// Storage configuration
	StoreType string `dynamodb:"store_type" json:"store_type"`         // "relational" | "kv" | "vector" | "timeseries" | "logs" | "passthrough"
	Authority string `dynamodb:"authority" json:"authority"`           // "system-of-record" | "derived"
	SchemaRef string `dynamodb:"schema_ref" json:"schema_ref"`         // pointer to schema definition

	// Hierarchy (for index pods)
	Kind_IsIndex bool `dynamodb:"-" json:"_is_index"`                  // convenience flag
	PartitionKey string `dynamodb:"partition_key" json:"partition_key"` // dimension for sharding (e.g., "namespace")
	Shards []ShardRef `dynamodb:"shards" json:"shards"`                // child pods (for index pods only)

	// Lifecycle
	Lifecycle string `dynamodb:"lifecycle" json:"lifecycle"`           // "active" | "merging" | "draining" | "retired"
	Freshness time.Time `dynamodb:"freshness" json:"freshness"`        // last modified timestamp

	// Metrics
	EventCount int64 `dynamodb:"event_count" json:"event_count"`       // number of events stored
	QueryStats QueryStats `dynamodb:"query_stats" json:"query_stats"` // hit/miss/latency

	// Metadata
	CreatedAt time.Time `dynamodb:"created_at" json:"created_at"`
	UpdatedAt time.Time `dynamodb:"updated_at" json:"updated_at"`
}

// ShardRef is a reference to a child pod (for index pods).
type ShardRef struct {
	ChildID    string `dynamodb:"child_id" json:"child_id"`           // pod ID of child
	Partition  string `dynamodb:"partition" json:"partition"`         // partition value (e.g., "namespace=prod")
	EventCount int64  `dynamodb:"event_count" json:"event_count"`     // events in this shard
}

// QueryStats tracks query performance metrics for a pod.
type QueryStats struct {
	Hits            int64 `dynamodb:"hits" json:"hits"`                         // successful queries
	Misses          int64 `dynamodb:"misses" json:"misses"`                     // queries that got no results
	P95LatencyMs    int64 `dynamodb:"p95_latency_ms" json:"p95_latency_ms"`     // 95th percentile latency
	TotalQueries    int64 `dynamodb:"total_queries" json:"total_queries"`
	LastUpdated     time.Time `dynamodb:"last_updated" json:"last_updated"`
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
