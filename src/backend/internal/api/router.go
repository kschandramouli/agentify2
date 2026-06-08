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

	// Admin: integrations management
	mux.HandleFunc("GET /admin/integrations", h.HandleIntegrationList)
	mux.HandleFunc("POST /admin/integrations", h.HandleIntegrationCreate)

	// Admin: pod registry (observability)
	mux.HandleFunc("GET /admin/pods", h.HandlePodRegistryList)
	mux.HandleFunc("GET /admin/pods/get", h.HandlePodRegistryGet)

	// Admin: tracked namespace/service pairs (powers frontend autocomplete)
	mux.HandleFunc("GET /admin/tracked", h.HandleTrackedEntities)

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
