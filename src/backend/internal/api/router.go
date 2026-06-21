package api

import (
	"log/slog"
	"net/http"

	"github.com/chan/agentify/backend/internal/telemetry"
)

// NewRouter creates a new HTTP router from a prebuilt handler.
func NewRouter(h *Handler, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", h.HandleHealth)

	// Event ingestion (for adapters to send data)
	mux.HandleFunc("POST /api/ingest", h.HandleIngestEvent)

	// Query endpoint (ops dashboard)
	mux.HandleFunc("POST /api/query", h.HandleQuery)

	// Agent tool callback: lets the Python agent fetch pod data during its loop
	mux.HandleFunc("POST /api/agent/fetch", h.HandleAgentFetch)

	// Admin: query history + metrics summary
	mux.HandleFunc("GET /admin/traces", h.HandleTraceList)
	mux.HandleFunc("GET /admin/traces/{id}", h.HandleTraceGet)
	mux.HandleFunc("GET /admin/metrics/summary", h.HandleMetricsSummary)

	// Admin: integrations management
	mux.HandleFunc("GET /admin/integrations", h.HandleIntegrationList)
	mux.HandleFunc("POST /admin/integrations", h.HandleIntegrationCreate)
	mux.HandleFunc("GET /admin/integrations/{id}", h.HandleIntegrationGet)
	mux.HandleFunc("PUT /admin/integrations/{id}", h.HandleIntegrationUpdate)
	mux.HandleFunc("DELETE /admin/integrations/{id}", h.HandleIntegrationDelete)

	// Admin: pod registry (observability)
	mux.HandleFunc("GET /admin/pods", h.HandlePodRegistryList)
	mux.HandleFunc("GET /admin/pods/get", h.HandlePodRegistryGet)

	// Admin: tracked namespace/service pairs (powers frontend autocomplete)
	mux.HandleFunc("GET /admin/tracked", h.HandleTrackedEntities)

	// Admin: sync — discover namespaces/services from the adapter (live K8s list)
	mux.HandleFunc("POST /admin/sync", h.HandleSyncNamespaces)
	mux.HandleFunc("GET /admin/sync", h.HandleSyncNamespaces) // also allow GET for CronJob curl

	// Chat: multi-turn conversational debugging
	mux.HandleFunc("POST /api/chat/sessions",          h.HandleCreateChatSession)
	mux.HandleFunc("GET /api/chat/sessions",           h.HandleListChatSessions)
	mux.HandleFunc("GET /api/chat/sessions/{id}",      h.HandleGetChatSession)
	mux.HandleFunc("POST /api/chat/sessions/{id}/messages", h.HandleSendChatMessage)
	mux.HandleFunc("DELETE /api/chat/sessions/{id}",   h.HandleDeleteChatSession)

	// Admin: model pricing — read/edit $/MTok rates shown in the UI and used for trace cost estimates
	mux.HandleFunc("GET /admin/pricing", h.HandleListPricing)
	mux.HandleFunc("PUT /admin/pricing", h.HandleUpsertPricing)
	mux.HandleFunc("POST /admin/pricing", h.HandleUpsertPricing)

	// Admin: on-demand cert renewal — issues from Vault PKI + updates K8s Secret
	mux.HandleFunc("POST /admin/certs/renew", h.HandleCertRenew)

	// TODO: add WebSocket handler for chat
	// mux.HandleFunc("/ws/chat", h.HandleChatWebSocket)

	// Outer mux: /metrics bypasses the logging+metrics middleware so scrapes
	// don't pollute request logs or the HTTP counters (ADR 0011). Everything else
	// goes through the middleware-wrapped API mux.
	root := http.NewServeMux()
	root.Handle("GET /metrics", telemetry.MetricsHandler())
	root.Handle("/", NewMiddleware(mux, logger))
	return root
}
