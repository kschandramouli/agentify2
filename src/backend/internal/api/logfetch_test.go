package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chan/agentify/backend/internal/governance"
)

// TestHandlePodLogs_ScrubsAtEgress verifies the on-demand log path (spec 008 /
// ADR 0014): the backend fetches a tail from the adapter and scrubs secrets before
// the agent sees them. A stub HTTP server stands in for the adapter (no cluster).
func TestHandlePodLogs_ScrubsAtEgress(t *testing.T) {
	const token = "s3cr3t-token"
	var gotAuth string
	// Stub adapter returns a raw log line containing a secret.
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logs" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(LogResponse{
			PodID:     "payment-7c9-bbb",
			Container: "app",
			Previous:  true,
			Logs:      "panic: dial tcp db:5432 password=hunter2 refused\nOOMKilled",
		})
	}))
	defer adapter.Close()

	h := &Handler{
		adapterClient: NewAdapterClient(adapter.URL, token),
		redactor:      governance.NewRedactor(true, false, slog.Default()),
		logger:        slog.Default(),
	}

	body := `{"tool":"get_pod_logs","args":{"pod_id":"payment-7c9-bbb","namespace":"prod","previous":true}}`
	req := httptest.NewRequest("POST", "/api/agent/fetch", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleAgentFetch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Tool string                 `json:"tool"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	logs, _ := resp.Data["logs"].(string)
	if strings.Contains(logs, "hunter2") {
		t.Errorf("secret leaked through egress: %q", logs)
	}
	// The crash reason must survive so the agent can use it.
	if !strings.Contains(logs, "OOMKilled") || !strings.Contains(logs, "panic") {
		t.Errorf("useful crash signal lost: %q", logs)
	}
	if resp.Data["previous"] != true {
		t.Errorf("previous flag not propagated: %v", resp.Data["previous"])
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("adapter did not receive bearer token, got %q", gotAuth)
	}
}

// TestHandlePodLogs_AdapterDownDegrades ensures an unreachable adapter degrades to
// an error payload (HTTP 200) rather than failing the agent's tool loop.
func TestHandlePodLogs_AdapterDownDegrades(t *testing.T) {
	h := &Handler{
		adapterClient: NewAdapterClient("http://127.0.0.1:0", ""), // unreachable
		redactor:      governance.NewRedactor(true, false, slog.Default()),
		logger:        slog.Default(),
	}
	body := `{"tool":"get_pod_logs","args":{"pod_id":"x","namespace":"prod"}}`
	req := httptest.NewRequest("POST", "/api/agent/fetch", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.HandleAgentFetch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degrade, not fail)", rec.Code)
	}
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp.Data["error"]; !ok {
		t.Errorf("expected an error payload, got %v", resp.Data)
	}
}
