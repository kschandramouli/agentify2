package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialCollector connects a real gorilla/websocket client to a test server
// wrapping CollectorHub.Register, simulating one cluster's collector.
func dialCollector(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestCollectorHubRequestLive(t *testing.T) {
	t.Run("no connection registered returns ErrClusterNotConnected", func(t *testing.T) {
		hub := NewCollectorHub()
		_, err := hub.RequestLive(context.Background(), "cluster-42", "live_list_pods", nil)
		if !errors.Is(err, ErrClusterNotConnected) {
			t.Fatalf("want ErrClusterNotConnected, got %v", err)
		}
	})

	t.Run("request/response round-trips by id", func(t *testing.T) {
		hub := NewCollectorHub()
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			hub.Register("cluster-42", conn)
		}))
		defer server.Close()

		clientConn := dialCollector(t, server)

		// Simulate the collector: read one request frame, echo back a result.
		go func() {
			var f liveFrame
			if err := clientConn.ReadJSON(&f); err != nil {
				return
			}
			result, _ := json.Marshal(map[string]string{"pod": "payment-api-abc123"})
			_ = clientConn.WriteJSON(liveFrame{ID: f.ID, Type: "response", Result: result})
		}()

		// Give the server side a moment to register before requesting.
		time.Sleep(100 * time.Millisecond)

		result, err := hub.RequestLive(context.Background(), "cluster-42", "live_list_pods", map[string]any{"namespace": "payments"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var decoded map[string]string
		if err := json.Unmarshal(result, &decoded); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if decoded["pod"] != "payment-api-abc123" {
			t.Errorf("result: want pod=payment-api-abc123, got %v", decoded)
		}
	})

	t.Run("collector error is surfaced", func(t *testing.T) {
		hub := NewCollectorHub()
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, _ := upgrader.Upgrade(w, r, nil)
			hub.Register("cluster-42", conn)
		}))
		defer server.Close()

		clientConn := dialCollector(t, server)
		go func() {
			var f liveFrame
			if err := clientConn.ReadJSON(&f); err != nil {
				return
			}
			_ = clientConn.WriteJSON(liveFrame{ID: f.ID, Type: "response", Error: "pod not found"})
		}()

		time.Sleep(100 * time.Millisecond)
		_, err := hub.RequestLive(context.Background(), "cluster-42", "live_list_pods", nil)
		if err == nil || !strings.Contains(err.Error(), "pod not found") {
			t.Fatalf("want error containing %q, got %v", "pod not found", err)
		}
	})

	t.Run("no response before the timeout returns ErrLiveRequestTimeout", func(t *testing.T) {
		hub := NewCollectorHub()
		hub.requestTimeout = 200 * time.Millisecond // shrink so the test doesn't take 15s
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, _ := upgrader.Upgrade(w, r, nil)
			hub.Register("cluster-42", conn)
		}))
		defer server.Close()

		dialCollector(t, server) // connects, but never answers

		time.Sleep(100 * time.Millisecond)
		_, err := hub.RequestLive(context.Background(), "cluster-42", "live_list_pods", nil)
		if !errors.Is(err, ErrLiveRequestTimeout) {
			t.Fatalf("want ErrLiveRequestTimeout, got %v", err)
		}
	})

	t.Run("reconnect replaces the previous connection for the same cluster", func(t *testing.T) {
		hub := NewCollectorHub()
		upgrader := websocket.Upgrader{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, _ := upgrader.Upgrade(w, r, nil)
			hub.Register("cluster-42", conn)
		}))
		defer server.Close()

		staleConn := dialCollector(t, server)
		time.Sleep(100 * time.Millisecond)

		freshConn := dialCollector(t, server)
		go func() {
			var f liveFrame
			if err := freshConn.ReadJSON(&f); err != nil {
				return
			}
			result, _ := json.Marshal(map[string]string{"from": "fresh"})
			_ = freshConn.WriteJSON(liveFrame{ID: f.ID, Type: "response", Result: result})
		}()
		time.Sleep(100 * time.Millisecond)

		// The stale connection should have been closed by the reconnect.
		staleConn.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := staleConn.ReadMessage(); err == nil {
			t.Error("expected the stale connection to be closed after a reconnect")
		}

		result, err := hub.RequestLive(context.Background(), "cluster-42", "live_list_pods", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var decoded map[string]string
		json.Unmarshal(result, &decoded)
		if decoded["from"] != "fresh" {
			t.Errorf("want the fresh connection to serve the request, got %v", decoded)
		}
	})
}
