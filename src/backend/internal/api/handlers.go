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
	pgstore "github.com/chan/agentify/backend/internal/storage/postgres"
	"github.com/chan/agentify/backend/internal/telemetry"
)

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	orch             *orchestrator.Router
	ingester         *ingestion.Ingester
	queryExec        *orchestrator.QueryExecutor
	agentClient      *AgentClient
	adapterClient    *AdapterClient
	redactor         *governance.Redactor
	integrationStore IntegrationStore // nil when postgres is not provisioned
	traceStore       TraceStore       // nil when postgres is not provisioned
	pricingStore     PricingStore     // nil when postgres is not provisioned
	logger           *slog.Logger
}

// NewHandler creates a new handler.
func NewHandler(orch *orchestrator.Router, agentServiceURL, adapterURL, adapterToken string, redactor *governance.Redactor, integrations IntegrationStore, traces TraceStore, pricing PricingStore, logger *slog.Logger) *Handler {
	ingester := ingestion.NewIngester(orch.GetPodRegistry(), orch.GetBackendFactory(), logger)
	queryExec := orchestrator.NewQueryExecutor(orch.GetPodRegistry(), orch.GetBackendFactory(), logger)

	return &Handler{
		orch:             orch,
		ingester:         ingester,
		queryExec:        queryExec,
		agentClient:      NewAgentClient(agentServiceURL),
		adapterClient:    NewAdapterClient(adapterURL, adapterToken),
		redactor:         redactor,
		integrationStore: integrations,
		traceStore:       traces,
		pricingStore:     pricing,
		logger:           logger,
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
		h.logTrace(traceID, req.Question, intent, namespace, "none", "error", nil, 0, nil, start, nil)
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
		h.logTrace(traceID, req.Question, intent, namespace, "no_data", "no_data", nil, 0, nil, start, nil)
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
		h.logTrace(traceID, req.Question, intent, namespace, "tier1", resp.Status, resp.Sources, resp.Confidence, nil, start, nil)
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
		h.logTrace(traceID, req.Question, intent, namespace, "tier2", "partial", resp.Sources, 0.5, nil, start, nil)
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
	h.logTrace(traceID, req.Question, intent, namespace, "tier2", agentStatus, agentResp.Sources, agentResp.Confidence, toolCallNames(agentResp.ToolCalls), start, agentResp)

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

// HandleIntegrationList returns all configured integrations (tokens redacted).
func (h *Handler) HandleIntegrationList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.integrationStore == nil {
		json.NewEncoder(w).Encode([]IntegrationResponse{})
		return
	}
	rows, err := h.integrationStore.ListIntegrations(r.Context())
	if err != nil {
		h.logger.Error("list integrations failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := make([]IntegrationResponse, len(rows))
	for i, row := range rows {
		resp[i] = integrationToResponse(row)
	}
	json.NewEncoder(w).Encode(resp)
}

// HandleIntegrationGet returns one integration by ID.
func (h *Handler) HandleIntegrationGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if h.integrationStore == nil {
		http.Error(w, "integration store not configured", http.StatusServiceUnavailable)
		return
	}
	row, err := h.integrationStore.GetIntegration(r.Context(), id)
	if err != nil {
		h.logger.Error("get integration failed", "id", id, "error", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(integrationToResponse(*row))
}

// integrationCreateRequest is the body accepted by POST /admin/integrations.
type integrationCreateRequest struct {
	Name       string   `json:"name"`
	AdapterURL string   `json:"adapter_url"`
	Namespaces []string `json:"namespaces"`
	Token      string   `json:"token"`
}

// HandleIntegrationCreate adds a new integration.
func (h *Handler) HandleIntegrationCreate(w http.ResponseWriter, r *http.Request) {
	if h.integrationStore == nil {
		http.Error(w, "integration store not configured", http.StatusServiceUnavailable)
		return
	}
	var req integrationCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.AdapterURL == "" {
		http.Error(w, "name and adapter_url are required", http.StatusBadRequest)
		return
	}
	if req.Namespaces == nil {
		req.Namespaces = []string{}
	}

	id := uuid.New().String()
	row := &pgstore.Integration{
		ID:         id,
		Name:       req.Name,
		AdapterURL: req.AdapterURL,
		Namespaces: req.Namespaces,
		Status:     "inactive",
		Token:      req.Token,
	}
	if err := h.integrationStore.CreateIntegration(r.Context(), row); err != nil {
		h.logger.Error("create integration failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	created, err := h.integrationStore.GetIntegration(r.Context(), id)
	if err != nil {
		h.logger.Error("fetch created integration failed", "id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(integrationToResponse(*created))
}

// integrationUpdateRequest is the body accepted by PUT /admin/integrations/{id}.
type integrationUpdateRequest struct {
	Name       string   `json:"name"`
	AdapterURL string   `json:"adapter_url"`
	Namespaces []string `json:"namespaces"`
	Status     string   `json:"status"`
	Token      string   `json:"token"` // empty = keep existing token
}

// HandleIntegrationUpdate replaces mutable fields for an existing integration.
func (h *Handler) HandleIntegrationUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if h.integrationStore == nil {
		http.Error(w, "integration store not configured", http.StatusServiceUnavailable)
		return
	}
	var req integrationUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.AdapterURL == "" {
		http.Error(w, "name and adapter_url are required", http.StatusBadRequest)
		return
	}
	if req.Namespaces == nil {
		req.Namespaces = []string{}
	}
	status := req.Status
	if status == "" {
		status = "inactive"
	}

	row := &pgstore.Integration{
		ID:         id,
		Name:       req.Name,
		AdapterURL: req.AdapterURL,
		Namespaces: req.Namespaces,
		Status:     status,
		Token:      req.Token,
	}
	if err := h.integrationStore.UpdateIntegration(r.Context(), row); err != nil {
		h.logger.Error("update integration failed", "id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	updated, err := h.integrationStore.GetIntegration(r.Context(), id)
	if err != nil {
		h.logger.Error("fetch updated integration failed", "id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(integrationToResponse(*updated))
}

// HandleIntegrationDelete removes an integration by ID.
func (h *Handler) HandleIntegrationDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if h.integrationStore == nil {
		http.Error(w, "integration store not configured", http.StatusServiceUnavailable)
		return
	}
	if err := h.integrationStore.DeleteIntegration(r.Context(), id); err != nil {
		h.logger.Error("delete integration failed", "id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// logTrace emits the per-query provenance record (spec 004): structured log +
// async Postgres insert so the HTTP response is never blocked.
// agentResp may be nil for Tier-1 / error paths (no LLM call was made).
func (h *Handler) logTrace(traceID, question, intent, namespace, tier, status string, sources []string, confidence float64, toolCalls []string, start time.Time, agentResp *AgentResponse) {
	latencyMs := time.Since(start).Milliseconds()
	var inTok, outTok, cacheWriteTok, cacheReadTok int64
	var cost float64
	if agentResp != nil {
		inTok          = agentResp.InputTokens
		outTok         = agentResp.OutputTokens
		cacheWriteTok  = agentResp.CacheCreationInputTokens
		cacheReadTok   = agentResp.CacheReadInputTokens
		cost           = agentResp.EstimatedCostUSD
	}
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
		"latency_ms", latencyMs,
		"input_tokens", inTok,
		"output_tokens", outTok,
		"cache_creation_input_tokens", cacheWriteTok,
		"cache_read_input_tokens", cacheReadTok,
		"estimated_cost_usd", cost,
	)
	if h.traceStore != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := h.traceStore.InsertTrace(ctx, pgstore.TraceRecord{
				ID:                       uuid.New().String(),
				TraceID:                  traceID,
				Question:                 question,
				Intent:                   intent,
				Namespace:                namespace,
				Tier:                     tier,
				Status:                   status,
				Confidence:               confidence,
				Sources:                  sources,
				ToolCalls:                toolCalls,
				LatencyMs:                latencyMs,
				StartedAt:                start,
				InputTokens:              inTok,
				OutputTokens:             outTok,
				CacheCreationInputTokens: cacheWriteTok,
				CacheReadInputTokens:     cacheReadTok,
				EstimatedCostUSD:         cost,
			}); err != nil {
				h.logger.Warn("trace persist failed", "error", err)
			}
		}()
	}
}

// HandleTraceList returns recent query traces for the admin history view.
func (h *Handler) HandleTraceList(w http.ResponseWriter, r *http.Request) {
	if h.traceStore == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]TraceResponse{})
		return
	}
	rows, err := h.traceStore.ListTraces(r.Context(), 200)
	if err != nil {
		h.logger.Error("list traces failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := make([]TraceResponse, len(rows))
	for i, row := range rows {
		resp[i] = traceToResponse(row)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// HandleTraceGet returns a single trace by its primary-key ID.
func (h *Handler) HandleTraceGet(w http.ResponseWriter, r *http.Request) {
	if h.traceStore == nil {
		http.Error(w, "trace store not available", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	rec, err := h.traceStore.GetTrace(r.Context(), id)
	if err != nil {
		h.logger.Warn("get trace failed", "id", id, "error", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, traceToResponse(*rec))
}

// HandleMetricsSummary returns aggregated query statistics for the metrics dashboard.
func (h *Handler) HandleMetricsSummary(w http.ResponseWriter, r *http.Request) {
	if h.traceStore == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(MetricsSummaryResponse{
			QueriesByTier:   map[string]int64{},
			QueriesByStatus: map[string]int64{},
			QueriesByIntent: map[string]int64{},
			CollectedAt:     time.Now(),
		})
		return
	}
	s, err := h.traceStore.GetTracesSummary(r.Context())
	if err != nil {
		h.logger.Error("metrics summary failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MetricsSummaryResponse{
		TotalQueries:      s.TotalQueries,
		Last24hCount:      s.Last24hCount,
		QueriesByTier:     s.QueriesByTier,
		QueriesByStatus:   s.QueriesByStatus,
		QueriesByIntent:   s.QueriesByIntent,
		AvgAgentLatencyMs: s.AvgAgentLatencyMs,
		P95AgentLatencyMs: s.P95AgentLatencyMs,
		CollectedAt:       time.Now(),
	})
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

// seedNamespaceCache writes the given discovered namespaces into current_state so
// TrackedEntities (and therefore the frontend autocomplete) reflects live data.
// Returns the number of services seeded.
func (h *Handler) seedNamespaceCache(ctx context.Context, namespaces []NamespaceEntry) int {
	kv, err := h.orch.GetBackendFactory().GetBackend("kv")
	if err != nil {
		return 0
	}
	seeder, ok := kv.(syncSeeder)
	if !ok {
		return 0
	}
	seeded := 0
	for _, ns := range namespaces {
		podID := "k8fy.live-state." + ns.Namespace
		for _, svc := range ns.Services {
			if _, serr := seeder.Store(ctx, podID, map[string]interface{}{
				"entity_key":      svc,
				"event_namespace": ns.Namespace,
				"type":            "service_synced",
				"source":          "sync",
				"payload":         map[string]interface{}{"name": svc},
			}); serr == nil {
				seeded++
			}
		}
	}
	return seeded
}

// SeedNamespaceCache runs in a background goroutine (called at startup and on
// demand when current_state is empty). It first checks whether current_state
// already has data — if so it exits immediately, which is the normal case after
// a pod restart where Postgres data survived the cycle. When current_state is
// empty it polls the adapter every 15 s for up to 5 minutes until the adapter
// responds, then seeds the cache.
func (h *Handler) SeedNamespaceCache(ctx context.Context) {
	// Skip if current_state already has entries — Postgres persists data across
	// pod restarts so this is usually true after a scale-up.
	if kv, kerr := h.orch.GetBackendFactory().GetBackend("kv"); kerr == nil {
		if p, ok := kv.(trackedEntitiesProvider); ok {
			if existing, eerr := p.TrackedEntities(ctx); eerr == nil && len(existing) > 0 {
				h.logger.Info("startup namespace sync: current_state already populated, skipping",
					"count", len(existing))
				return
			}
		}
	}

	const retryInterval = 15 * time.Second // tighter than before (was 30 s)
	const maxAttempts = 20                 // 5 minutes total
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		namespaces, err := h.adapterClient.DiscoverNamespaces(ctx)
		if err != nil {
			h.logger.Info("startup namespace sync: adapter not ready, will retry",
				"attempt", attempt, "max", maxAttempts, "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryInterval):
			}
			continue
		}
		seeded := h.seedNamespaceCache(ctx, namespaces)
		h.logger.Info("startup namespace sync complete",
			"namespaces", len(namespaces), "services", seeded)
		return
	}
	h.logger.Warn("startup namespace sync: adapter did not become available within 5 minutes")
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

	seeded := h.seedNamespaceCache(r.Context(), namespaces)
	h.logger.Info("seeded current_state from sync", "namespaces", len(namespaces), "services", seeded)

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

// syncSeeder is satisfied by *postgres.CurrentState — it lets HandleSyncNamespaces
// write discovered entities directly into current_state so the frontend autocomplete
// reflects live adapter data without waiting for ingestion events to re-arrive.
type syncSeeder interface {
	Store(ctx context.Context, podID string, data map[string]interface{}) (string, error)
}

// HandleTrackedEntities returns all known namespace/service pairs from the
// live-state current_state table. Powers the frontend search autocomplete.
//
// After a scale-up the table may be empty until the adapter re-emits events.
// Rather than returning [] silently, the handler falls back to a live adapter
// call (3 s timeout) and seeds current_state in the same request, so the first
// page load after a scale-up always returns real data. If the adapter is not
// yet ready the response is [] and a background seed is queued so the 3 s
// frontend re-poll (active while the list is empty) picks up the data shortly.
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

	// Empty current_state — try a live adapter call so the first request after
	// a scale-up returns real data without the user having to wait.
	if len(entities) == 0 {
		syncCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		namespaces, aerr := h.adapterClient.DiscoverNamespaces(syncCtx)
		cancel()
		if aerr == nil && len(namespaces) > 0 {
			h.seedNamespaceCache(r.Context(), namespaces)
			entities, _ = provider.TrackedEntities(r.Context())
			h.logger.Info("tracked entities: live-seeded from adapter", "count", len(entities))
		} else {
			// Adapter not ready yet — seed in the background so the frontend's
			// 3 s re-poll (active while the list is empty) picks it up shortly.
			go h.SeedNamespaceCache(context.Background())
			h.logger.Info("tracked entities: adapter not ready, seeding in background", "error", aerr)
		}
	}

	if entities == nil {
		entities = []string{}
	}
	writeJSON(w, http.StatusOK, entities)
}

// HandleListPricing returns all model pricing rows from the database.
// The Python agent and the Admin UI both consume this endpoint.
func (h *Handler) HandleListPricing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.pricingStore == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	rows, err := h.pricingStore.ListModelPricing(r.Context())
	if err != nil {
		h.logger.Warn("failed to list model pricing", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []pgstore.ModelPricing{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// HandleUpsertPricing inserts or updates a single model pricing row.
// Body: { model_id, display_name, input_per_mtok, output_per_mtok, cache_write_per_mtok, cache_read_per_mtok }
func (h *Handler) HandleUpsertPricing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.pricingStore == nil {
		http.Error(w, "pricing store not available", http.StatusServiceUnavailable)
		return
	}
	var p pgstore.ModelPricing
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if p.ModelID == "" {
		http.Error(w, "model_id is required", http.StatusBadRequest)
		return
	}
	if err := h.pricingStore.UpsertModelPricing(r.Context(), &p); err != nil {
		h.logger.Warn("failed to upsert model pricing", "model_id", p.ModelID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
