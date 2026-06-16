package api

import (
	"context"
	"time"

	pgstore "github.com/chan/agentify/backend/internal/storage/postgres"
)

// TraceStore is the query-history CRUD interface implemented by the Postgres client.
type TraceStore interface {
	InsertTrace(ctx context.Context, t pgstore.TraceRecord) error
	ListTraces(ctx context.Context, limit int) ([]pgstore.TraceRecord, error)
	GetTrace(ctx context.Context, id string) (*pgstore.TraceRecord, error)
	GetTracesSummary(ctx context.Context) (*pgstore.TracesSummary, error)
}

// TraceResponse is the API representation of one query trace row.
type TraceResponse struct {
	ID                       string    `json:"id"`
	TraceID                  string    `json:"trace_id"`
	Question                 string    `json:"question"`
	Intent                   string    `json:"intent"`
	Namespace                string    `json:"namespace"`
	Tier                     string    `json:"tier"`
	Status                   string    `json:"status"`
	Confidence               float64   `json:"confidence"`
	Sources                  []string  `json:"sources"`
	ToolCalls                []string  `json:"tool_calls"`
	LatencyMs                int64     `json:"latency_ms"`
	StartedAt                time.Time `json:"started_at"`
	CreatedAt                time.Time `json:"created_at"`
	InputTokens              int64     `json:"input_tokens"`
	OutputTokens             int64     `json:"output_tokens"`
	CacheCreationInputTokens int64     `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64     `json:"cache_read_input_tokens"`
	EstimatedCostUSD         float64   `json:"estimated_cost_usd"`
}

// MetricsSummaryResponse is returned by GET /admin/metrics/summary.
type MetricsSummaryResponse struct {
	TotalQueries      int64            `json:"total_queries"`
	Last24hCount      int64            `json:"last_24h_count"`
	QueriesByTier     map[string]int64 `json:"queries_by_tier"`
	QueriesByStatus   map[string]int64 `json:"queries_by_status"`
	QueriesByIntent   map[string]int64 `json:"queries_by_intent"`
	AvgAgentLatencyMs float64          `json:"avg_agent_latency_ms"`
	P95AgentLatencyMs float64          `json:"p95_agent_latency_ms"`
	CollectedAt       time.Time        `json:"collected_at"`
}

func traceToResponse(t pgstore.TraceRecord) TraceResponse {
	src := t.Sources
	if src == nil {
		src = []string{}
	}
	tc := t.ToolCalls
	if tc == nil {
		tc = []string{}
	}
	return TraceResponse{
		ID: t.ID, TraceID: t.TraceID, Question: t.Question, Intent: t.Intent,
		Namespace: t.Namespace, Tier: t.Tier, Status: t.Status,
		Confidence: t.Confidence, Sources: src, ToolCalls: tc,
		LatencyMs: t.LatencyMs, StartedAt: t.StartedAt, CreatedAt: t.CreatedAt,
		InputTokens:              t.InputTokens,
		OutputTokens:             t.OutputTokens,
		CacheCreationInputTokens: t.CacheCreationInputTokens,
		CacheReadInputTokens:     t.CacheReadInputTokens,
		EstimatedCostUSD:         t.EstimatedCostUSD,
	}
}

// ChatStore is the multi-turn conversation interface implemented by the Postgres client.
type ChatStore interface {
	CreateChatSession(ctx context.Context, s *pgstore.ChatSession) error
	GetChatSession(ctx context.Context, id string) (*pgstore.ChatSession, error)
	UpdateChatSession(ctx context.Context, s *pgstore.ChatSession) error
	ListChatSessions(ctx context.Context, limit int) ([]pgstore.ChatSession, error)
	DeleteChatSession(ctx context.Context, id string) error
}

// ChatSessionResponse is the API shape of a chat session.
type ChatSessionResponse struct {
	ID         string                `json:"id"`
	Title      string                `json:"title"`
	Namespace  string                `json:"namespace"`
	Service    string                `json:"service"`
	Messages   []pgstore.ChatMessage `json:"messages"`
	CreatedAt  time.Time             `json:"created_at"`
	LastActive time.Time             `json:"last_active"`
}

func chatSessionToResponse(s pgstore.ChatSession) ChatSessionResponse {
	return ChatSessionResponse{
		ID: s.ID, Title: s.Title, Namespace: s.Namespace, Service: s.Service,
		Messages: s.Messages, CreatedAt: s.CreatedAt, LastActive: s.LastActive,
	}
}

// PricingStore is the model-pricing CRUD interface implemented by the Postgres client.
type PricingStore interface {
	ListModelPricing(ctx context.Context) ([]pgstore.ModelPricing, error)
	UpsertModelPricing(ctx context.Context, p *pgstore.ModelPricing) error
}

// IntegrationStore is the integration CRUD interface implemented by the Postgres
// client. Using an interface keeps the handler decoupled from the storage package
// and makes the nil-safe "not configured" path cheap.
type IntegrationStore interface {
	ListIntegrations(ctx context.Context) ([]pgstore.Integration, error)
	GetIntegration(ctx context.Context, id string) (*pgstore.Integration, error)
	CreateIntegration(ctx context.Context, in *pgstore.Integration) error
	UpdateIntegration(ctx context.Context, in *pgstore.Integration) error
	DeleteIntegration(ctx context.Context, id string) error
}

// IntegrationResponse is what the API returns — identical to pgstore.Integration
// but with HasToken instead of Token so the credential is never leaked.
type IntegrationResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	AdapterURL string    `json:"adapter_url"`
	Namespaces []string  `json:"namespaces"`
	Status     string    `json:"status"`
	HasToken   bool      `json:"has_token"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// integrationToResponse converts a storage integration to an API response,
// replacing the plaintext token with a boolean.
func integrationToResponse(in pgstore.Integration) IntegrationResponse {
	return IntegrationResponse{
		ID:         in.ID,
		Name:       in.Name,
		AdapterURL: in.AdapterURL,
		Namespaces: in.Namespaces,
		Status:     in.Status,
		HasToken:   in.Token != "",
		CreatedAt:  in.CreatedAt,
		UpdatedAt:  in.UpdatedAt,
	}
}

// QueryRequest represents a user query.
type QueryRequest struct {
	Question string                 `json:"question"`
	Context  map[string]interface{} `json:"context"`
}

// ToolCallInfo describes one tool call made by Claude during reasoning.
type ToolCallInfo struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// QueryResponse represents the answer to a query.
type QueryResponse struct {
	Answer     string                 `json:"answer"`
	Status     string                 `json:"status"`
	Confidence float64                `json:"confidence"`
	Sources    []string               `json:"sources"`
	TraceID    string                 `json:"trace_id,omitempty"` // provenance correlation id (spec 004)
	ToolCalls  []ToolCallInfo         `json:"tool_calls,omitempty"` // tools Claude called during reasoning
	Details    map[string]interface{} `json:"details,omitempty"`    // structured data for UI rendering
}
