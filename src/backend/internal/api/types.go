package api

import (
	"context"
	"time"

	pgstore "github.com/chan/agentify/backend/internal/storage/postgres"
)

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
