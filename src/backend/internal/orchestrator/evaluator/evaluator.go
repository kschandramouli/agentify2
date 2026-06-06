// Package evaluator implements the deterministic health/cert rules used by the
// Tier-1 (no-LLM) query fast-path. See context-mesh/decisions/0006-two-tier-query-path.md.
//
// These are the Go port of the pure functions in src/agent/k8fy/reasoning.py and
// the health model in context-mesh/specs/002-k8fy-health-queries.md. Keeping them
// in the backend (where pod data is fetched) lets deterministic queries answer in
// milliseconds without an API call.
package evaluator

import (
	"encoding/json"
	"fmt"
)

// Health status values.
const (
	StatusHealthy   = "healthy"
	StatusDegraded  = "degraded"
	StatusUnhealthy = "unhealthy"
	StatusUnknown   = "unknown"
)

// RenewThresholdDays is the certificate renewal window.
const RenewThresholdDays = 30

// PodResult is a single pod's evaluated health.
type PodResult struct {
	Entity string
	Status string
	Reason string
}

// PodHealth applies the pod health model to a pod payload:
//   - UNHEALTHY: CrashLoopBackOff/OOMKilled, or phase Failed/Unknown
//   - DEGRADED:  Running but not Ready, or restarts >= 3
//   - HEALTHY:   Running, Ready, restarts < 3
func PodHealth(payload map[string]interface{}) (status, reason string) {
	phase := getString(payload, "phase")
	reasonField := getString(payload, "reason")
	ready := getBool(payload, "ready")
	restarts := getInt(payload, "restarts")

	switch {
	case reasonField == "CrashLoopBackOff" || reasonField == "OOMKilled":
		return StatusUnhealthy, reasonField
	case phase == "Failed" || phase == "Unknown":
		return StatusUnhealthy, "phase " + phase
	case phase == "Running":
		if !ready {
			return StatusDegraded, "running but not ready"
		}
		if restarts >= 3 {
			return StatusDegraded, fmt.Sprintf("%d restarts", restarts)
		}
		return StatusHealthy, "running and ready"
	default:
		return StatusUnknown, "phase " + phase
	}
}

// ServiceStatus aggregates pod results into a service verdict:
//   - UNHEALTHY: 0 healthy pods
//   - HEALTHY:   >= 75% of pods healthy
//   - DEGRADED:  at least one healthy, but < 75%
func ServiceStatus(results []PodResult) (status string, healthy int, readyRatio float64) {
	if len(results) == 0 {
		return StatusUnknown, 0, 0
	}
	for _, r := range results {
		if r.Status == StatusHealthy {
			healthy++
		}
	}
	ratio := float64(healthy) / float64(len(results))
	switch {
	case healthy == 0:
		return StatusUnhealthy, healthy, ratio
	case ratio >= 0.75:
		return StatusHealthy, healthy, ratio
	default:
		return StatusDegraded, healthy, ratio
	}
}

// CertRenewal applies the 30-day renewal rule to a certificate payload.
func CertRenewal(payload map[string]interface{}) (shouldRenew bool, days int, reason string) {
	days = getInt(payload, "days_until_expiry")
	switch {
	case days < 0:
		return true, days, "already expired"
	case days < RenewThresholdDays:
		return true, days, fmt.Sprintf("expires in %d days", days)
	default:
		return false, days, fmt.Sprintf("valid for %d more days", days)
	}
}

// --- payload accessors (tolerant of JSON-decoded types) ---

func getString(m map[string]interface{}, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]interface{}, k string) bool {
	if v, ok := m[k].(bool); ok {
		return v
	}
	return false
}

// getInt tolerates JSON's float64, plus int/int64/json.Number.
func getInt(m map[string]interface{}, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}
