package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	pgstore "github.com/chan/agentify/backend/internal/storage/postgres"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHandleCollectorConnect(t *testing.T) {
	t.Run("no Authorization header is rejected", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{}, collectorHub: NewCollectorHub(), logger: testLogger()}
		req := httptest.NewRequest(http.MethodGet, "/api/collector/connect", nil)
		w := httptest.NewRecorder()

		h.HandleCollectorConnect(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status: want %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("unrecognized bearer token is rejected", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{}, collectorHub: NewCollectorHub(), logger: testLogger()}
		req := httptest.NewRequest(http.MethodGet, "/api/collector/connect", nil)
		req.Header.Set("Authorization", "Bearer nonexistent-token")
		w := httptest.NewRecorder()

		h.HandleCollectorConnect(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status: want %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})
}

// TestLiveFetchEndToEnd exercises the full glue: a fake collector connects
// via HandleCollectorConnect, then HandleLiveFetch relays a request to it and
// returns the collector's answer over plain HTTP — the path the Python agent
// actually uses.
func TestLiveFetchEndToEnd(t *testing.T) {
	store := &fakeIntegrationStore{
		byToken: map[string]*pgstore.Integration{
			"real-token": {ID: "cluster-42", TenantID: "tenant-a"},
		},
	}
	h := &Handler{integrationStore: store, collectorHub: NewCollectorHub(), logger: testLogger()}

	connectServer := httptest.NewServer(http.HandlerFunc(h.HandleCollectorConnect))
	defer connectServer.Close()

	wsURL := "ws" + strings.TrimPrefix(connectServer.URL, "http")
	header := http.Header{"Authorization": []string{"Bearer real-token"}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	go func() {
		var f liveFrame
		if err := conn.ReadJSON(&f); err != nil {
			return
		}
		result, _ := json.Marshal(map[string]string{"phase": "Running"})
		_ = conn.WriteJSON(liveFrame{ID: f.ID, Type: "response", Result: result})
	}()
	time.Sleep(100 * time.Millisecond) // let the server-side goroutine register the connection

	body, _ := json.Marshal(liveFetchRequest{ClusterID: "cluster-42", Tool: "live_list_pods", Args: map[string]any{"namespace": "payments"}})
	req := httptest.NewRequest(http.MethodPost, "/api/live-fetch", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleLiveFetch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want %d, got %d (body=%s)", http.StatusOK, w.Code, w.Body.String())
	}
	var decoded map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if decoded["phase"] != "Running" {
		t.Errorf("response body: want phase=Running, got %v", decoded)
	}
}

func TestHandleLiveFetchValidation(t *testing.T) {
	h := &Handler{collectorHub: NewCollectorHub(), logger: testLogger()}

	t.Run("missing cluster_id is rejected", func(t *testing.T) {
		body, _ := json.Marshal(liveFetchRequest{Tool: "live_list_pods"})
		req := httptest.NewRequest(http.MethodPost, "/api/live-fetch", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.HandleLiveFetch(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status: want %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("unsupported tool is rejected", func(t *testing.T) {
		body, _ := json.Marshal(liveFetchRequest{ClusterID: "cluster-42", Tool: "rotate_vault_cert"})
		req := httptest.NewRequest(http.MethodPost, "/api/live-fetch", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.HandleLiveFetch(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status: want %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("live_get_certificates (ADR 0024) is an allowed tool", func(t *testing.T) {
		body, _ := json.Marshal(liveFetchRequest{ClusterID: "cluster-99", Tool: "live_get_certificates"})
		req := httptest.NewRequest(http.MethodPost, "/api/live-fetch", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.HandleLiveFetch(w, req)

		// Not connected (no collector registered for cluster-99), but
		// specifically NOT the "unsupported tool" 400 — proves the tool
		// passed the allow-list check.
		if w.Code != http.StatusBadGateway {
			t.Fatalf("status: want %d (cluster not connected, not unsupported-tool), got %d", http.StatusBadGateway, w.Code)
		}
	})

	t.Run("cluster not connected returns 502", func(t *testing.T) {
		body, _ := json.Marshal(liveFetchRequest{ClusterID: "cluster-99", Tool: "live_list_pods"})
		req := httptest.NewRequest(http.MethodPost, "/api/live-fetch", bytes.NewReader(body))
		w := httptest.NewRecorder()

		h.HandleLiveFetch(w, req)

		if w.Code != http.StatusBadGateway {
			t.Fatalf("status: want %d, got %d", http.StatusBadGateway, w.Code)
		}
	})

	t.Run("wrong HTTP method is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/live-fetch", nil)
		w := httptest.NewRecorder()

		h.HandleLiveFetch(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status: want %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
	})
}
