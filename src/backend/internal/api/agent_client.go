package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AgentClient communicates with the Python agent service.
type AgentClient struct {
	baseURL string
	client  *http.Client
}

// NewAgentClient creates a new agent client.
func NewAgentClient(baseURL string) *AgentClient {
	return &AgentClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 180 * time.Second, // Opus 4.8 takes 60-90s; frontend waits 190s
		},
	}
}

// AgentRequest is sent to the agent service.
type AgentRequest struct {
	Intent  string                 `json:"intent"`
	Data    map[string]interface{} `json:"data"`
	Context map[string]interface{} `json:"context"`
	TraceID string                 `json:"trace_id,omitempty"` // propagated for cross-service correlation (spec 004)
}

// AgentToolCall is a tool the agent invoked (for provenance).
type AgentToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// AgentResponse is returned from the agent service.
type AgentResponse struct {
	Answer           string                 `json:"answer"`
	Status           string                 `json:"status"` // "healthy","degraded","unhealthy","error","unknown"
	Reasoning        string                 `json:"reasoning"`
	Confidence       float64                `json:"confidence"`
	Sources          []string               `json:"sources"`
	ToolCalls        []AgentToolCall        `json:"tool_calls"`
	Details          map[string]interface{} `json:"details"` // severity, likely_cause, recommendations, findings (spec 005)
	InputTokens      int64                  `json:"input_tokens"`
	OutputTokens     int64                  `json:"output_tokens"`
	EstimatedCostUSD float64                `json:"estimated_cost_usd"`
}

// Reason calls the agent service to reason about the data.
func (ac *AgentClient) Reason(question string, intent string, data map[string]interface{}, context map[string]interface{}, traceID string) (*AgentResponse, error) {
	req := AgentRequest{
		Intent:  intent,
		Data:    data,
		Context: context,
		TraceID: traceID,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", ac.baseURL+"/reason", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := ac.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent service returned %d: %s", resp.StatusCode, string(body))
	}

	var agentResp AgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &agentResp, nil
}
