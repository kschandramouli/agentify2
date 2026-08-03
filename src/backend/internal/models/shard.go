package models

import "strings"

// PodID builds a pod (shard) ID from its parts, skipping any empty ones —
// the mechanism behind ADR 0024's cluster-aware pod IDs. Called with an
// empty clusterID (every deployment that hasn't registered a fleet cluster)
// it reproduces today's plain shape exactly:
//
//	PodID("k8fy.live-state", "", "payments")        -> "k8fy.live-state.payments"
//	PodID("k8fy.live-state", "cluster-42", "payments") -> "k8fy.live-state.cluster-42.payments"
//	PodID("k8fy.certificates", "")                  -> "k8fy.certificates"
//	PodID("k8fy.certificates", "cluster-42")        -> "k8fy.certificates.cluster-42"
//
// One function covers both the namespace-sharded pods (live-state, etc.)
// and the single-global pods (certificates) — cluster scoping is just
// another optional segment, not a different mechanism.
func PodID(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, ".")
}
