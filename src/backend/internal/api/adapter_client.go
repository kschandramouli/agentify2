package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AdapterClient calls the K8fy adapter's on-demand log endpoint (spec 008 / ADR
// 0014). The adapter is the only component with a Kubernetes client, so the
// backend fetches pod logs through it. Returned log text is ephemeral — it is
// scrubbed at the egress boundary and never persisted.
type AdapterClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewAdapterClient creates an adapter client. An empty baseURL disables log fetch.
// token, when set, is sent as a Bearer credential guarding the adapter's /logs.
func NewAdapterClient(baseURL, token string) *AdapterClient {
	return &AdapterClient{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: 12 * time.Second},
	}
}

// LogRequest asks the adapter for a bounded log tail.
type LogRequest struct {
	PodID     string `json:"pod_id"`
	Namespace string `json:"namespace,omitempty"`
	Container string `json:"container,omitempty"`
	Previous  bool   `json:"previous,omitempty"`
	TailLines int    `json:"tail_lines,omitempty"`
}

// LogResponse is the adapter's reply (raw, pre-redaction).
type LogResponse struct {
	PodID     string `json:"pod_id"`
	Namespace string `json:"namespace"`
	Container string `json:"container"`
	Previous  bool   `json:"previous"`
	Logs      string `json:"logs"`
	Error     string `json:"error,omitempty"`
}

// FetchLogs requests a log tail from the adapter.
func (ac *AdapterClient) FetchLogs(ctx context.Context, req LogRequest) (*LogResponse, error) {
	if ac.baseURL == "" {
		return nil, fmt.Errorf("adapter URL not configured")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal log request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", ac.baseURL+"/logs", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create log request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if ac.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+ac.token)
	}

	resp, err := ac.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("adapter request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("adapter returned %d: %s", resp.StatusCode, string(b))
	}
	var out LogResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("failed to decode log response: %w", err)
	}
	return &out, nil
}
