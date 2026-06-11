package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/chan/agentify/backend/internal/governance"
	"github.com/chan/agentify/backend/internal/ingestion"
	"github.com/chan/agentify/backend/internal/models"
	"github.com/chan/agentify/backend/internal/orchestrator"
	"github.com/chan/agentify/backend/internal/telemetry"
)

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	orch          *orchestrator.Router
	ingester      *ingestion.Ingester
	queryExec     *orchestrator.QueryExecutor
	agentClient   *AgentClient
	adapterClient *AdapterClient
	redactor      *governance.Redactor
	logger        *slog.Logger
}

// NewHandler creates a new handler.
func NewHandler(orch *orchestrator.Router, agentServiceURL, adapterURL, adapterToken string, redactor *governance.Redactor, logger *slog.Logger) *Handler {
	ingester := ingestion.NewIngester(orch.GetPodRegistry(), orch.GetBackendFactory(), logger)
	queryExec := orchestrator.NewQueryExecutor(orch.GetPodRegistry(), orch.GetBackendFactory(), logger)

	return &Handler{
		orch:          orch,
		ingester:      ingester,
		queryExec:     queryExec,
		agentClient:   NewAgentClient(agentServiceURL),
		adapterClient: NewAdapterClient(adapterURL, adapterToken),
		redactor:      redactor,
		logger:        logger,
	}
}

// HandleHealth responds with service health status.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// HandleIngestEvent accepts a canonical event and ingests it into the mesh.
func (h *Handler) HandleIngestEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var event models.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		h.logger.Error("failed to decode event", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Ingest the event
	result, err := h.ingester.Ingest(r.Context(), &event)
	if err != nil {
		h.logger.Error("ingestion failed", "event_id", event.ID, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(result)
}

// HandleQuery processes a user query and returns an answer.
func (h *Handler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	start := time.Now()
	traceID := uuid.New().String() // provenance correlation id (spec 004)

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("failed to decode query request", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Extract namespace from context (default to "prod")
	namespace := "prod"
	if ns, ok := req.Context["namespace"]; ok {
		namespace = ns.(string)
	}

	// Extract intent or infer from question
	// For MVP: use simple heuristics to determine intent
	intent := inferIntent(req.Question)

	// Route to pods and fetch data
	pods, err := h.queryExec.RouteToPods(r.Context(), intent, namespace)
	if err != nil {
		h.logger.Error("failed to route query", "error", err)
		telemetry.QueriesTotal.WithLabelValues(intent, "none", "error").Inc()
		h.logTrace(traceID, req.Question, intent, namespace, "none", "error", nil, 0, nil, start)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if len(pods) == 0 {
		resp := QueryResponse{
			Answer:     "No data available for this query",
			Status:     "no_data",
			Confidence: 0.0,
			Sources:    []string{},
			TraceID:    traceID,
		}
		telemetry.QueriesTotal.WithLabelValues(intent, "no_data", "no_data").Inc()
		h.logTrace(traceID, req.Question, intent, namespace, "no_data", "no_data", nil, 0, nil, start)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Build the backend query. By default it carries no lookup key, so KV pods
	// return every entity in the shard for the agent to reason over. A caller may
	// target a single entity by putting its exact stored key under "entity"/"key"
	// in the request context (we don't guess a key from fuzzy service names).
	podQuery := buildPodQuery(req.Context)

	// Fetch from pods
	podData := make(map[string]interface{})
	for _, pod := range pods {
		data, err := h.queryExec.FetchFromPod(r.Context(), pod, podQuery)
		if err != nil {
			h.logger.Warn("failed to fetch from pod", "pod_id", pod.ID, "error", err)
			continue
		}
		podData[pod.ID] = map[string]interface{}{
			"data": data,
			"type": pod.StoreType,
			"tags": pod.Tags,
		}
	}

	// Tier 1 — deterministic fast-path (ADR 0006): answer structured intents
	// (health/cert) directly from the data with no LLM call. Falls through to the
	// agent when the intent needs synthesis or there's no data to evaluate.
	if resp, handled := tryDeterministic(intent, podData, req.Context); handled {
		resp.TraceID = traceID
		h.logger.Info("answered via deterministic fast-path", "intent", intent, "pods", len(pods))
		telemetry.QueriesTotal.WithLabelValues(intent, "tier1", "ok").Inc()
		telemetry.QueryDuration.WithLabelValues("tier1").Observe(time.Since(start).Seconds())
		h.logTrace(traceID, req.Question, intent, namespace, "tier1", resp.Status, resp.Sources, resp.Confidence, nil, start)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Tier 2 — agentic synthesis path. Redact at the egress boundary (ADR 0007):
	// the agent (and the model it calls) only ever sees allowlisted data.
	// trimAgentPayload then reduces token footprint: dedup by entity_key,
	// drop completed-rollout noise, hard-cap at maxEventsPerPod per pod.
	agentData := trimAgentPayload(h.redactor.RedactPodData(podData))
	agentStart := time.Now()
	agentResp, err := h.agentClient.Reason(req.Question, intent, agentData, req.Context, traceID)
	telemetry.AgentCallDuration.Observe(time.Since(agentStart).Seconds())
	if err != nil {
		h.logger.Warn("agent service error", "error", err)
		telemetry.AgentCallsTotal.WithLabelValues("error").Inc()
		telemetry.QueriesTotal.WithLabelValues(intent, "tier2", "partial").Inc()
		telemetry.QueryDuration.WithLabelValues("tier2").Observe(time.Since(start).Seconds())
		// Fallback: return raw data
		resp := QueryResponse{
			Answer:     formatPodData(podData),
			Status:     "partial",
			Confidence: 0.5,
			Sources:    extractPodIDs(pods),
			TraceID:    traceID,
		}
		h.logTrace(traceID, req.Question, intent, namespace, "tier2", "partial", resp.Sources, 0.5, nil, start)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
		return
	}

	telemetry.AgentCallsTotal.WithLabelValues("ok").Inc()
	telemetry.QueriesTotal.WithLabelValues(intent, "tier2", "ok").Inc()
	telemetry.QueryDuration.WithLabelValues("tier2").Observe(time.Since(start).Seconds())

	// Return agent response. Pass the agent's status through so the frontend
	// can render error/degraded cards correctly. Fall back to "ok" only when
	// the agent omits the field (older image compatibility).
	agentStatus := agentResp.Status
	if agentStatus == "" {
		agentStatus = "ok"
	}

	var toolCalls []ToolCallInfo
	for _, tc := range agentResp.ToolCalls {
		toolCalls = append(toolCalls, ToolCallInfo{Name: tc.Name, Arguments: tc.Arguments})
	}

	resp := QueryResponse{
		Answer:     agentResp.Answer,
		Status:     agentStatus,
		Confidence: agentResp.Confidence,
		Sources:    agentResp.Sources,
		TraceID:    traceID,
		ToolCalls:  toolCalls,
		Details:    agentResp.Details,
	}
	h.logTrace(traceID, req.Question, intent, namespace, "tier2", agentStatus, agentResp.Sources, agentResp.Confidence, toolCallNames(agentResp.ToolCalls), start)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// HandleAgentFetch lets the Python agent fetch pod data on demand during its
// tool-calling loop. The agent's tools (get_service_health, query_pod,
// get_certificates, get_pod_events) map to a pod query here. This endpoint only
// reads data — it never re-invokes the agent, so there is no recursion.
func (h *Handler) HandleAgentFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool string                 `json:"tool"`
		Args map[string]interface{} `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("failed to decode agent fetch", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Logs are not in storage: they are fetched live from the adapter, scrubbed,
	// and returned without persisting (spec 008 / ADR 0014).
	if req.Tool == "get_pod_logs" {
		h.handlePodLogs(w, r, req.Args)
		return
	}

	intent, namespace, key := mapToolToQuery(req.Tool, req.Args)

	pods, err := h.queryExec.RouteToPods(r.Context(), intent, namespace)
	if err != nil {
		h.logger.Warn("agent fetch routing failed", "tool", req.Tool, "error", err)
		// Degrade to an empty result rather than failing the agent's loop.
		writeJSON(w, http.StatusOK, map[string]interface{}{"tool": req.Tool, "data": map[string]interface{}{}})
		return
	}

	query := map[string]interface{}{}
	if key != "" {
		query["key"] = key
	}
	// Forward time-window / entity / order filters so history tools (spec 006) can
	// read a windowed series; the events store ignores any it doesn't recognize.
	for _, p := range []string{"since", "until", "order", "type", "entity", "pod_id", "service", "deployment"} {
		if v := stringArg(req.Args, p); v != "" {
			query[p] = v
		}
	}
	// get_service_health passes "service_name" (not "service") — map it explicitly
	// so CurrentState.Query can do the service-prefix scan for Deployment-only
	// workloads whose pods are stored as "{service}-{rs-hash}-{pod-hash}".
	if req.Tool == "get_service_health" {
		if svcName := stringArg(req.Args, "service_name"); svcName != "" {
			query["service"] = svcName
		}
	}
	if v, ok := req.Args["limit"]; ok {
		query["limit"] = v
	}

	data := make(map[string]interface{})
	for _, pod := range pods {
		rows, err := h.queryExec.FetchFromPod(r.Context(), pod, query)
		if err != nil {
			h.logger.Warn("agent fetch from pod failed", "pod_id", pod.ID, "error", err)
			continue
		}
		data[pod.ID] = rows
	}

	// Redact at the egress boundary (ADR 0007) before the agent (and its model) sees it.
	writeJSON(w, http.StatusOK, map[string]interface{}{"tool": req.Tool, "data": h.redactor.RedactFetch(data)})
}

// handlePodLogs fetches a bounded log tail from the adapter, scrubs it at the
// egress boundary (best-effort text redaction — ADR 0014), and returns it without
// persisting. A failure degrades to an empty/error payload so the agent loop
// continues rather than crashing.
func (h *Handler) handlePodLogs(w http.ResponseWriter, r *http.Request, args map[string]interface{}) {
	podID := stringArg(args, "pod_id")
	if podID == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"tool": "get_pod_logs",
			"data": map[string]interface{}{"error": "pod_id required"},
		})
		return
	}

	tail := 100
	switch v := args["tail_lines"].(type) {
	case float64:
		tail = int(v)
	case int:
		tail = v
	}

	resp, err := h.adapterClient.FetchLogs(r.Context(), LogRequest{
		PodID:     podID,
		Namespace: stringArg(args, "namespace"),
		Container: stringArg(args, "container"),
		Previous:  boolArg(args, "previous"),
		TailLines: tail,
	})
	if err != nil {
		h.logger.Warn("adapter log fetch failed", "pod_id", podID, "error", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"tool": "get_pod_logs",
			"data": map[string]interface{}{"error": "logs unavailable"},
		})
		return
	}

	out := map[string]interface{}{
		"pod_id":    resp.PodID,
		"container": resp.Container,
		"previous":  resp.Previous,
		"logs":      h.redactor.RedactText(resp.Logs), // scrub before the agent sees it
	}
	if resp.Error != "" {
		out["error"] = resp.Error
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tool": "get_pod_logs", "data": out})
}

// mapToolToQuery translates an agent tool name + args into a (intent, namespace,
// entity-key) query triple aligned with the K8fy pod taxonomy (ADR 0005).
func mapToolToQuery(tool string, args map[string]interface{}) (intent, namespace, key string) {
	namespace = stringArg(args, "namespace")
	if namespace == "" {
		namespace = "prod"
	}

	switch tool {
	case "get_certificates":
		return "cert_check", namespace, ""
	case "get_service_health":
		// Don't use an exact key lookup — for Deployment-only workloads (queue
		// workers, consumers) there is no service_* row keyed by the plain service
		// name; only pod_* rows keyed by the full pod name exist. An exact match
		// returns nothing. Return "" so HandleAgentFetch will use the service-prefix
		// LIKE scan path in CurrentState.Query instead.
		return "health_check", namespace, ""
	case "query_pod", "get_pod_events":
		return "health_check", namespace, stringArg(args, "pod_id")
	case "get_metrics_history":
		// Time-series of restart samples; the entity is forwarded as a filter
		// (not a point-lookup key) by HandleAgentFetch (spec 006).
		return "metrics_history", namespace, ""
	case "get_change_history":
		// Deploy/change events; entity (deployment/service) forwarded as a filter (spec 007).
		return "change_history", namespace, ""
	default:
		return "general_query", namespace, ""
	}
}

// stringArg safely extracts a string argument from a decoded JSON map.
func stringArg(args map[string]interface{}, name string) string {
	if v, ok := args[name].(string); ok {
		return v
	}
	return ""
}

// boolArg safely extracts a boolean argument from a decoded JSON map.
func boolArg(args map[string]interface{}, name string) bool {
	if v, ok := args[name].(bool); ok {
		return v
	}
	return false
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// HandleIntegrationList returns all configured integrations.
func (h *Handler) HandleIntegrationList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// TODO: fetch integrations from DynamoDB
	integrations := []map[string]interface{}{}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(integrations)
}

// HandleIntegrationCreate adds a new integration.
func (h *Handler) HandleIntegrationCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var integration map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&integration); err != nil {
		h.logger.Error("failed to decode integration", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// TODO: validate and store integration in DynamoDB
	// TODO: emit event to SQS for adapter initialization

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(integration)
}

// HandlePodRegistryList returns all pods in the registry.
func (h *Handler) HandlePodRegistryList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pods, err := h.orch.GetPodRegistry().ListActivePods(r.Context())
	if err != nil {
		h.logger.Error("failed to list pods", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(pods)
}

// inferIntent determines the query intent from natural language.
func inferIntent(question string) string {
	lower := strings.ToLower(question)
	// Diagnostic phrasing must win BEFORE health/cert so "why is payment unhealthy?"
	// fans out to a multi-signal Tier-2 diagnosis (spec 005) rather than dropping
	// into the single-signal Tier-1 health fast-path (resolves spec 003 limitation #3).
	for _, kw := range []string{"why", "what's wrong", "whats wrong", "what is wrong", "root cause", "root-cause", "diagnose", "diagnos", "investigate", "going on", "going wrong", "broken"} {
		if strings.Contains(lower, kw) {
			return "diagnose"
		}
	}
	if strings.Contains(lower, "health") || strings.Contains(lower, "healthy") {
		return "health_check"
	}
	if strings.Contains(lower, "certificate") || strings.Contains(lower, "cert") || strings.Contains(lower, "expir") {
		return "cert_check"
	}
	if strings.Contains(lower, "metric") || strings.Contains(lower, "cpu") || strings.Contains(lower, "memory") {
		return "metrics_query"
	}
	return "general_query"
}

// buildPodQuery derives the backend query map from the request context.
// It forwards an explicit entity lookup key (from "key" or "entity") so KV pods
// can do a point lookup; absent that, the empty key makes them scan the whole
// shard. It deliberately does not derive a key from "service"/"namespace", which
// are filters the agent applies, not exact storage keys.
func buildPodQuery(reqContext map[string]interface{}) map[string]interface{} {
	query := make(map[string]interface{})
	for k, v := range reqContext {
		query[k] = v
	}
	if _, ok := query["key"]; !ok {
		if entity, ok := query["entity"].(string); ok && entity != "" {
			query["key"] = entity
		}
	}
	return query
}

// logTrace emits the per-query provenance record (spec 004). v1 is a structured
// log line; app-level retrieval-by-id is a documented follow-up.
func (h *Handler) logTrace(traceID, question, intent, namespace, tier, status string, sources []string, confidence float64, toolCalls []string, start time.Time) {
	h.logger.Info("query.trace",
		"trace_id", traceID,
		"question", question,
		"intent", intent,
		"namespace", namespace,
		"tier", tier,
		"status", status,
		"sources", sources,
		"confidence", confidence,
		"tool_calls", toolCalls,
		"latency_ms", time.Since(start).Milliseconds(),
	)
}

// toolCallNames extracts the tool names the agent invoked, for the trace.
func toolCallNames(calls []AgentToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return names
}

// formatPodData returns a brief human-readable summary of the fetched pod data.
// Called only when the agent service is unavailable; output is shown as the
// fallback answer so the user knows what data was fetched but not analysed.
func formatPodData(podData map[string]interface{}) string {
	var sb strings.Builder

	// Count total events across all pods to distinguish "no data found" from
	// "data found but Claude couldn't process it".
	totalEvents := 0
	for _, raw := range podData {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		events, _ := m["data"].([]interface{})
		totalEvents += len(events)
	}

	if totalEvents == 0 {
		sb.WriteString("No data found for this service. Check that the namespace and service name are correct, " +
			"and that the adapter is syncing this namespace.")
		return sb.String()
	}

	sb.WriteString("Agent service unavailable — data was collected but not analysed by Claude.\n\n")
	for podID, raw := range podData {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		events, _ := m["data"].([]interface{})
		if len(events) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("• %s  (%d events)\n", podID, len(events)))
		for _, e := range events {
			ev, ok := e.(map[string]interface{})
			if !ok {
				continue
			}
			payload, _ := ev["payload"].(map[string]interface{})
			evType, _ := ev["type"].(string)
			entityKey, _ := ev["entity_key"].(string)

			switch evType {
			case "pod_modified", "pod_added", "pod_deleted":
				restarts, _ := payload["restarts"].(float64)
				ready, _ := payload["ready"].(bool)
				phase, _ := payload["phase"].(string)
				sb.WriteString(fmt.Sprintf(
					"  – %s  %-12s  phase=%-10s  ready=%-5v  restarts=%.0f\n",
					evType, entityKey, phase, ready, restarts,
				))
			case "service_added":
				clusterIP, _ := payload["cluster_ip"].(string)
				sb.WriteString(fmt.Sprintf("  – %s  %s  ip=%s\n", evType, entityKey, clusterIP))
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// extractPodIDs extracts pod IDs from a slice of pods.
func extractPodIDs(pods []*models.Pod) []string {
	ids := make([]string, len(pods))
	for i, pod := range pods {
		ids[i] = pod.ID
	}
	return ids
}

// HandlePodRegistryGet returns a single pod.
func (h *Handler) HandlePodRegistryGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	podID := r.URL.Query().Get("id")
	if podID == "" {
		http.Error(w, "missing pod id", http.StatusBadRequest)
		return
	}

	pod, err := h.orch.GetPodRegistry().GetPod(r.Context(), podID)
	if err != nil {
		h.logger.Error("failed to get pod", "pod_id", podID, "error", err)
		http.Error(w, "pod not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(pod)
}

// HandleSyncNamespaces calls the adapter to discover current K8s namespaces +
// services, and returns them so the frontend search autocomplete can be updated
// immediately. The CronJob calls the same endpoint on a schedule.
func (h *Handler) HandleSyncNamespaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	namespaces, err := h.adapterClient.DiscoverNamespaces(r.Context())
	if err != nil {
		h.logger.Warn("namespace discovery failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "adapter unavailable — namespace discovery requires adapter to be running",
		})
		return
	}

	// Flatten to namespace/service strings for the frontend autocomplete.
	suggestions := make([]string, 0)
	for _, ns := range namespaces {
		for _, svc := range ns.Services {
			suggestions = append(suggestions, ns.Namespace+"/"+svc)
		}
		if len(ns.Services) == 0 {
			// Namespace exists but no services yet — still useful to surface.
			suggestions = append(suggestions, ns.Namespace+"/")
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"namespaces":  namespaces,
		"suggestions": suggestions,
		"total":       len(namespaces),
	})
}

// trackedEntitiesProvider is satisfied by *postgres.CurrentState.
type trackedEntitiesProvider interface {
	TrackedEntities(ctx context.Context) ([]string, error)
}

// HandleTrackedEntities returns all known namespace/service pairs from the
// live-state current_state table. Powers the frontend search autocomplete.
func (h *Handler) HandleTrackedEntities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// The kv backend wraps *postgres.CurrentState which implements TrackedEntities.
	kv, err := h.orch.GetBackendFactory().GetBackend("kv")
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	provider, ok := kv.(trackedEntitiesProvider)
	if !ok {
		writeJSON(w, http.StatusOK, []string{})
		return
	}

	entities, err := provider.TrackedEntities(r.Context())
	if err != nil {
		h.logger.Warn("failed to list tracked entities", "error", err)
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	if entities == nil {
		entities = []string{}
	}
	writeJSON(w, http.StatusOK, entities)
}
