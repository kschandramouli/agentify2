package config

import "strings"

// EventProfile is the concrete form of the "event profile" described in
// context-mesh/policies/storage-strategy.md. It is keyed by event_namespace
// (a logical grouping) — never by event type — so adding an integration means
// registering a profile, not editing routing logic ("classify, don't enumerate").
//
// The realized store types and sharding here match the K8fy worked example in
// storage-strategy.md and the lifecycle table in pod-formation.md. Where a
// policy-preferred backend isn't wired yet, the profile falls back to an
// available store (see ADR 0005).
type EventProfile struct {
	EventNamespace string // e.g. "k8fy.live-state"
	Integration    string // logical grouping → pod.Namespace, e.g. "k8fy"
	StoreType      string // realized store family: "kv" | "relational" | "vector" | ...
	PodHint        string // "by-namespace" | "by-time-window" | "single"
	Sharded        bool   // true → events fan out to per-partition leaf shards under an index pod
	PartitionField string // payload field used as the shard partition (e.g. "namespace")
}

// profiles holds the registered event profiles. For the MVP these are declared
// in code; the design intent (storage-strategy.md Step 3) is for integrations to
// register them at runtime and for the refinement loop to tune them.
var profiles = map[string]EventProfile{
	// Live cluster state — current health of pods/services, sharded by K8s
	// namespace so "health of service X in prod" hits exactly one shard.
	"k8fy.live-state": {
		EventNamespace: "k8fy.live-state",
		Integration:    "k8fy",
		StoreType:      "kv",
		PodHint:        "by-namespace",
		Sharded:        true,
		PartitionField: "namespace",
	},
	// Certificate expiry — current state of each secret's expiry date.
	// Stored as kv (latest-wins) so repeated scrapes overwrite rather than
	// accumulate; each secret is one row keyed by secret name.
	"k8fy.certificates": {
		EventNamespace: "k8fy.certificates",
		Integration:    "k8fy",
		StoreType:      "kv",
		PodHint:        "single",
		Sharded:        false,
	},
	// Cluster events — restart/failure history beyond K8s' ~1h retention.
	// Policy prefers a log/search index; falls back to relational until one exists.
	"k8fy.events": {
		EventNamespace: "k8fy.events",
		Integration:    "k8fy",
		StoreType:      "relational",
		PodHint:        "by-time-window",
		Sharded:        false,
	},
	// Metric samples — append-only restart-count time-series (spec 006). Policy
	// prefers a TSDB; lands in the Postgres events table for v1 (ADR 0013).
	"k8fy.metrics": {
		EventNamespace: "k8fy.metrics",
		Integration:    "k8fy",
		StoreType:      "relational",
		PodHint:        "by-time-window",
		Sharded:        false,
	},
}

// LookupProfile returns the registered profile for an event namespace.
// Unknown namespaces return ok=false; callers fall back to trait-based
// classification (the storage-strategy decision function).
func LookupProfile(eventNamespace string) (EventProfile, bool) {
	p, ok := profiles[eventNamespace]
	return p, ok
}

// IntegrationOf returns the integration grouping for an event namespace — the
// segment before the first dot (e.g. "k8fy.live-state" → "k8fy").
func IntegrationOf(eventNamespace string) string {
	if i := strings.IndexByte(eventNamespace, '.'); i > 0 {
		return eventNamespace[:i]
	}
	return eventNamespace
}
