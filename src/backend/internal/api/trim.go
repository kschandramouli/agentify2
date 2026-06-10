package api

import "fmt"

// maxEventsPerPod is the hard cap on events sent to the agent per pod after
// deduplication and noise removal. Keeps the per-request token footprint
// predictable regardless of how much history the pod has accumulated.
const maxEventsPerPod = 15

// trimAgentPayload reduces the token footprint of redacted pod data before it
// is forwarded to the agent service. It operates on the same map structure
// produced by FetchFromPod: map[podID]{"data":[]events,"type":str,"tags":[]}.
//
// For KV-store pods (live-state): deduplicates by entity_key (latest event
// per entity wins) then drops low-signal completed pods. These two steps
// account for the bulk of token inflation in the payments/live-state pod:
// 13 raw events → ~3 after trim (the crashing workers + service record).
//
// A hard cap of maxEventsPerPod applies after the above to guard against
// unexpectedly large payloads from relational or high-volume pods.
func trimAgentPayload(podData map[string]interface{}) map[string]interface{} {
	trimmed := make(map[string]interface{}, len(podData))
	for podID, raw := range podData {
		m, ok := raw.(map[string]interface{})
		if !ok {
			trimmed[podID] = raw
			continue
		}
		events, _ := m["data"].([]interface{})
		storeType, _ := m["type"].(string)

		if storeType == "kv" {
			events = deduplicateByEntityKey(events)
			events = dropCompletedNoise(events)
		}

		// Hard cap: keep the most recent maxEventsPerPod events. Events are
		// ordered oldest-first (adapter appends in arrival order), so slicing
		// from the tail gives the most recent.
		if len(events) > maxEventsPerPod {
			events = events[len(events)-maxEventsPerPod:]
		}

		trimmed[podID] = map[string]interface{}{
			"data": events,
			"type": storeType,
			"tags": m["tags"],
		}
	}
	return trimmed
}

// deduplicateByEntityKey keeps only the most recent event per entity_key.
// Events are assumed to be in arrival order (oldest first), so iterating
// forward and overwriting leaves the latest event for each key.
func deduplicateByEntityKey(events []interface{}) []interface{} {
	order := make([]string, 0, len(events))
	latest := make(map[string]interface{}, len(events))

	for _, e := range events {
		ev, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		key, _ := ev["entity_key"].(string)
		if key == "" {
			key = fmt.Sprintf("__unnamed_%d", len(latest))
		}
		if _, seen := latest[key]; !seen {
			order = append(order, key)
		}
		latest[key] = e
	}

	result := make([]interface{}, 0, len(order))
	for _, k := range order {
		result = append(result, latest[k])
	}
	return result
}

// dropCompletedNoise removes pod_deleted events for pods that completed
// cleanly (phase=Succeeded, restarts=0). These are old rollout pods shed
// during a deployment — they carry no diagnostic signal and are the main
// source of token inflation in live-state KV pods.
//
// Deleted events are kept when restarts > 0 or phase != Succeeded so that
// crash-looping pods that were subsequently deleted are not silently dropped.
func dropCompletedNoise(events []interface{}) []interface{} {
	result := make([]interface{}, 0, len(events))
	for _, e := range events {
		ev, ok := e.(map[string]interface{})
		if !ok {
			result = append(result, e)
			continue
		}
		evType, _ := ev["type"].(string)
		if evType != "pod_deleted" {
			result = append(result, e)
			continue
		}
		payload, _ := ev["payload"].(map[string]interface{})
		phase, _ := payload["phase"].(string)
		restarts, _ := payload["restarts"].(float64)
		if phase == "Succeeded" && restarts == 0 {
			continue // clean rollout shedding — drop
		}
		result = append(result, e)
	}
	return result
}
