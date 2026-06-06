package models

import (
	"time"
)

// Event is a canonical event representation that flows through the system.
// All incoming events (from K8s, CRM, webhooks, etc.) are normalized to this structure.
//
// See context-mesh/policies/storage-strategy.md for the event profile concept.
type Event struct {
	// Identification
	ID        string    `json:"id"`        // unique event ID (UUID)
	Timestamp time.Time `json:"timestamp"` // when the event occurred

	// Classification (from event profile)
	EventNamespace string                 `json:"event_namespace"` // e.g., "k8fy.events", "crm.ticket"
	Type           string                 `json:"type"`            // event type (e.g., "pod_restart", "ticket_created")
	Source         string                 `json:"source"`          // originating system (e.g., "kubernetes-api", "salesforce")

	// Data
	Payload map[string]interface{} `json:"payload"` // the event data itself
	Text    *string                `json:"text"`    // optional: freeform text (for vector storage)

	// EntityKey is the stable identity of the thing this event describes (e.g. a
	// pod or service name). For current-state stores it is the storage key, so the
	// latest event for an entity overwrites the previous one (latest-wins). Empty
	// for pure-history events, which are keyed by event ID instead. See ADR 0005.
	EntityKey string `json:"entity_key"`

	// Traits (from storage-strategy classification)
	Traits EventTraits `json:"traits"`

	// Metadata
	CorrelationID *string `json:"correlation_id"` // for tracing across pods
	UserID        *string `json:"user_id"`        // who triggered this
	Tags          []string `json:"tags"`          // searchable tags
}

// EventTraits describes how an event should be stored.
// See context-mesh/policies/storage-strategy.md for trait definitions.
type EventTraits struct {
	// Shape: structured | semi-structured | freeform-text | numeric/metric
	Shape string `json:"shape"`

	// Access pattern: point-lookup | similarity/semantic | filter+aggregate | relationship-traversal | time-range-scan
	AccessPattern string `json:"access_pattern"`

	// Temporality: current-state | append-only | time-series
	Temporality string `json:"temporality"`

	// Mutability: immutable | mutable | ephemeral
	Mutability string `json:"mutability"`

	// Authority: system-of-record | derived
	Authority string `json:"authority"`

	// Retention: how long to keep this event (e.g., "30d", "90d", "never")
	Retention string `json:"retention"`
}

// EventIngestionResult is the response after an event is ingested.
type EventIngestionResult struct {
	EventID     string    `json:"event_id"`
	PodID       string    `json:"pod_id"`
	CreatedPod  bool      `json:"created_pod"`   // true if a new pod was created
	StoreType   string    `json:"store_type"`
	Latency     int64     `json:"latency_ms"`
	Timestamp   time.Time `json:"timestamp"`
}
