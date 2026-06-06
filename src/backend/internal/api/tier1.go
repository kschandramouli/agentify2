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
func tryDeterministic(intent string, podData map[string]interface{}) (QueryResponse, bool) {
	switch intent {
	case "health_check":
		return tier1Health(podData)
	case "cert_check":
		return tier1Cert(podData)
	default:
		// metrics_query, general_query, etc. need synthesis — Tier 2.
		return QueryResponse{}, false
	}
}

func tier1Health(podData map[string]interface{}) (QueryResponse, bool) {
	var results []evaluator.PodResult
	forEachRow(podData, func(entity string, payload map[string]interface{}) {
		status, reason := evaluator.PodHealth(payload)
		results = append(results, evaluator.PodResult{Entity: entity, Status: status, Reason: reason})
	})
	if len(results) == 0 {
		return QueryResponse{}, false // nothing to evaluate — let the agent try its tools
	}

	status, healthy, ratio := evaluator.ServiceStatus(results)
	var b strings.Builder
	fmt.Fprintf(&b, "Service is %s: %d of %d pod(s) healthy (%.0f%%).",
		strings.ToUpper(status), healthy, len(results), ratio*100)
	for _, r := range results {
		if r.Status != evaluator.StatusHealthy {
			fmt.Fprintf(&b, " %s is %s (%s).", r.Entity, r.Status, r.Reason)
		}
	}

	return QueryResponse{
		Answer:     b.String(),
		Status:     "ok",
		Confidence: 1.0, // deterministic rule applied to the data — not an estimate
		Sources:    sourceKeys(podData),
	}, true
}

func tier1Cert(podData map[string]interface{}) (QueryResponse, bool) {
	type certResult struct {
		entity      string
		shouldRenew bool
		reason      string
	}
	var certs []certResult
	forEachRow(podData, func(entity string, payload map[string]interface{}) {
		renew, _, reason := evaluator.CertRenewal(payload)
		certs = append(certs, certResult{entity, renew, reason})
	})
	if len(certs) == 0 {
		return QueryResponse{}, false
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

	return QueryResponse{
		Answer:     b.String(),
		Status:     "ok",
		Confidence: 1.0,
		Sources:    sourceKeys(podData),
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
