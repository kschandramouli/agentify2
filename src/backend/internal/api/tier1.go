package api

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chan/agentify/backend/internal/orchestrator/evaluator"
)

// tryDeterministic answers structured intents directly from fetched store data
// without an LLM call (Tier 1 — see context-mesh/decisions/0006-two-tier-query-path.md).
// Returns (response, true) when it can answer; (_, false) to fall through to the
// agent (Tier 2) — e.g. free-text/compound intents, or when there's no data to
// evaluate.
func tryDeterministic(intent string, podData map[string]interface{}, reqCtx map[string]interface{}) (QueryResponse, bool) {
	switch intent {
	case "health_check":
		service, _ := reqCtx["service"].(string)
		return tier1Health(podData, service)
	case "cert_check":
		hint, _ := reqCtx["service_hint"].(string)
		return tier1Cert(podData, hint)
	default:
		// metrics_query, general_query, etc. need synthesis — Tier 2.
		return QueryResponse{}, false
	}
}

// podPhase extracts the phase string from a pod payload.
func podPhase(payload map[string]interface{}) string {
	if p, ok := payload["phase"].(string); ok {
		return p
	}
	return ""
}

func tier1Health(podData map[string]interface{}, serviceFilter string) (QueryResponse, bool) {
	type podEntry struct {
		name      string
		status    string
		reason    string
		phase     string
		restarts  int
		ready     bool
		completed bool // Succeeded/Pending-without-phase → skip from health calc
	}

	var active []evaluator.PodResult
	var allPods []podEntry

	forEachRow(podData, func(entity string, payload map[string]interface{}) {
		if !entityMatchesService(entity, serviceFilter) {
			return
		}
		phase := podPhase(payload)
		ready, _ := payload["ready"].(bool)
		restarts := 0
		switch v := payload["restarts"].(type) {
		case float64:
			restarts = int(v)
		case int:
			restarts = v
		}

		// Succeeded = completed pod from a finished rollout or Job.
		// It's not a running service instance — exclude from health calculation.
		if phase == "Succeeded" || phase == "Completed" {
			allPods = append(allPods, podEntry{
				name: entity, status: "completed", reason: "phase " + phase,
				phase: phase, completed: true,
			})
			return
		}
		// Skip empty-phase entries (usually stale service-level objects, not pods)
		if phase == "" {
			return
		}

		status, reason := evaluator.PodHealth(payload)
		active = append(active, evaluator.PodResult{Entity: entity, Status: status, Reason: reason})
		allPods = append(allPods, podEntry{
			name: entity, status: status, reason: reason,
			phase: phase, ready: ready, restarts: restarts,
		})
	})

	if len(active) == 0 {
		return QueryResponse{}, false
	}

	svcStatus, healthy, ratio := evaluator.ServiceStatus(active)

	// Summary answer (still plain text for fallback/accessibility)
	completed := len(allPods) - len(active)
	var b strings.Builder
	fmt.Fprintf(&b, "Service is %s: %d of %d active pod(s) healthy (%.0f%%).",
		strings.ToUpper(svcStatus), healthy, len(active), ratio*100)
	if completed > 0 {
		fmt.Fprintf(&b, " %d completed/old pod(s) excluded from health score.", completed)
	}
	for _, r := range active {
		if r.Status != evaluator.StatusHealthy {
			fmt.Fprintf(&b, " %s is %s (%s).", r.Entity, r.Status, r.Reason)
		}
	}

	// Structured details — rendered by the frontend into a scannable layout
	podList := make([]map[string]interface{}, 0, len(allPods))
	for _, p := range allPods {
		podList = append(podList, map[string]interface{}{
			"name":      p.name,
			"status":    p.status,
			"reason":    p.reason,
			"phase":     p.phase,
			"ready":     p.ready,
			"restarts":  p.restarts,
			"completed": p.completed,
		})
	}

	return QueryResponse{
		Answer:     b.String(),
		Status:     svcStatus, // "healthy"|"degraded"|"unhealthy" — drives NEEDS_CLAUDE in the frontend
		Confidence: 1.0,
		Sources:    sourceKeys(podData),
		Details: map[string]interface{}{
			"healthy":         healthy,
			"total_active":    len(active),
			"total_completed": completed,
			"ratio":           ratio,
			"service_status":  svcStatus,
			"pods":            podList,
		},
	}, true
}

// certMatchesService returns true if the secret name is plausibly related to the
// given service — matches common naming conventions like "payment-tls",
// "payment.tls", "tls-payment". Empty serviceHint means all certs match.
func certMatchesService(secretName, serviceHint string) bool {
	if serviceHint == "" {
		return true
	}
	sn := strings.ToLower(secretName)
	sh := strings.ToLower(serviceHint)
	return strings.Contains(sn, sh) || strings.Contains(sh, sn)
}

func tier1Cert(podData map[string]interface{}, serviceHint string) (QueryResponse, bool) {
	type certResult struct {
		entity      string
		shouldRenew bool
		days        int
		expiresAt   string
		reason      string
		namespace   string
	}

	// Collect all certs (deduped — first entry per name is most recent).
	seen := make(map[string]bool)
	var allCerts []certResult
	forEachRow(podData, func(entity string, payload map[string]interface{}) {
		if seen[entity] {
			return
		}
		seen[entity] = true
		renew, days, reason := evaluator.CertRenewal(payload)
		expiresAt, _ := payload["expires_at"].(string)
		ns, _ := payload["namespace"].(string)
		allCerts = append(allCerts, certResult{
			entity: entity, shouldRenew: renew, days: days,
			expiresAt: expiresAt, reason: reason, namespace: ns,
		})
	})

	// Prefer certs whose name matches the service hint; fall back to all certs.
	certs := allCerts
	if serviceHint != "" {
		var matched []certResult
		for _, c := range allCerts {
			if certMatchesService(c.entity, serviceHint) {
				matched = append(matched, c)
			}
		}
		if len(matched) > 0 {
			certs = matched
		}
	}

	if len(certs) == 0 {
		// Return a clean Tier-1 "not applicable" — don't fall through to Tier-2
		// where Claude writes a paragraph explaining the empty namespace.
		return QueryResponse{
			Answer:     "Certificate inventory for this service is empty — no TLS secrets found in this namespace.",
			Status:     "not_applicable",
			Confidence: 1.0,
			Sources:    sourceKeys(podData),
			Details: map[string]interface{}{
				"certs_checked":          0,
				"certs_needing_renewal":  0,
				"renewal_threshold_days": evaluator.RenewThresholdDays,
				"certificates":           []interface{}{},
			},
		}, true
	}

	renewCount := 0
	for _, c := range certs {
		if c.shouldRenew {
			renewCount++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d certificate(s) checked; %d need renewal (< %d days).",
		len(certs), renewCount, evaluator.RenewThresholdDays)
	for _, c := range certs {
		if c.shouldRenew {
			fmt.Fprintf(&b, " %s: %s.", c.entity, c.reason)
		}
	}

	certList := make([]map[string]interface{}, 0, len(certs))
	for _, c := range certs {
		urgency := "ok"
		if c.shouldRenew {
			if c.days < 7 {
				urgency = "crit"
			} else {
				urgency = "warn"
			}
		}
		certList = append(certList, map[string]interface{}{
			"name": c.entity, "namespace": c.namespace,
			"should_renew": c.shouldRenew, "days": c.days,
			"expires_at": c.expiresAt, "reason": c.reason, "urgency": urgency,
		})
	}

	return QueryResponse{
		Answer:     b.String(),
		Status:     "ok",
		Confidence: 1.0,
		Sources:    sourceKeys(podData),
		Details: map[string]interface{}{
			"certs_checked":          len(certs),
			"certs_needing_renewal":  renewCount,
			"renewal_threshold_days": evaluator.RenewThresholdDays,
			"certificates":           certList,
		},
	}, true
}

// forEachRow walks fetched pod data (pod_id -> {data: [rows], ...}) and invokes
// fn with each row's entity key and payload.
func forEachRow(podData map[string]interface{}, fn func(entity string, payload map[string]interface{})) {
	for _, v := range podData {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		rows, ok := entry["data"].([]map[string]interface{})
		if !ok {
			continue
		}
		for _, row := range rows {
			payload, ok := row["payload"].(map[string]interface{})
			if !ok {
				continue
			}
			entity, _ := row["entity_key"].(string)
			if entity == "" {
				if entity, _ = payload["pod_id"].(string); entity == "" {
					entity, _ = payload["secret"].(string)
				}
			}
			fn(entity, payload)
		}
	}
}

func sourceKeys(podData map[string]interface{}) []string {
	keys := make([]string, 0, len(podData))
	for k := range podData {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// entityMatchesService returns true when entity is (a) an exact match for
// service, or (b) a Kubernetes pod whose name is {service}-{rs-hash} or
// {service}-{rs-hash}-{pod-hash} (≤1 hyphen after the service prefix).
//
// This prevents "payment" from matching "payment-worker-<hash>-<hash>":
// stripping the "payment-" prefix leaves "worker-<hash>-<hash>", which
// contains 2 hyphens — more than the 1 allowed for a direct k8s pod.
// An empty service means "match everything".
func entityMatchesService(entity, service string) bool {
	if service == "" {
		return true
	}
	el := strings.ToLower(entity)
	sl := strings.ToLower(service)
	if el == sl {
		return true
	}
	prefix := sl + "-"
	if !strings.HasPrefix(el, prefix) {
		return false
	}
	return strings.Count(el[len(prefix):], "-") <= 1
}
