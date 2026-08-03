package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleResolveCluster exercises the P16 service->cluster resolver
// (ADR 0023) — the agent's degrade-to-today's-behavior contract depends on
// "no match" being a normal 200 with an empty list, never an error.
func TestHandleResolveCluster(t *testing.T) {
	t.Run("missing namespace is rejected", func(t *testing.T) {
		h := &Handler{clusterServiceStore: &fakeClusterServiceStore{}}
		req := httptest.NewRequest(http.MethodGet, "/api/resolve-cluster?service=payment-api", nil)
		w := httptest.NewRecorder()

		h.HandleResolveCluster(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status: want %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("missing service is rejected", func(t *testing.T) {
		h := &Handler{clusterServiceStore: &fakeClusterServiceStore{}}
		req := httptest.NewRequest(http.MethodGet, "/api/resolve-cluster?namespace=payments", nil)
		w := httptest.NewRecorder()

		h.HandleResolveCluster(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status: want %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("no match returns 200 with an empty list, not an error", func(t *testing.T) {
		h := &Handler{clusterServiceStore: &fakeClusterServiceStore{}}
		req := httptest.NewRequest(http.MethodGet, "/api/resolve-cluster?namespace=payments&service=unknown-svc", nil)
		w := httptest.NewRecorder()

		h.HandleResolveCluster(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status: want %d, got %d", http.StatusOK, w.Code)
		}
		var resp resolveClusterResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.ClusterIDs) != 0 {
			t.Errorf("ClusterIDs: want empty, got %v", resp.ClusterIDs)
		}
	})

	t.Run("single match resolves to one cluster", func(t *testing.T) {
		h := &Handler{clusterServiceStore: &fakeClusterServiceStore{
			resolved: map[string][]string{"payments/payment-api": {"cluster-42"}},
		}}
		req := httptest.NewRequest(http.MethodGet, "/api/resolve-cluster?namespace=payments&service=payment-api", nil)
		w := httptest.NewRecorder()

		h.HandleResolveCluster(w, req)

		var resp resolveClusterResponse
		json.NewDecoder(w.Body).Decode(&resp)
		if len(resp.ClusterIDs) != 1 || resp.ClusterIDs[0] != "cluster-42" {
			t.Errorf("ClusterIDs: want [cluster-42], got %v", resp.ClusterIDs)
		}
	})

	t.Run("ambiguous match resolves to every matching cluster", func(t *testing.T) {
		h := &Handler{clusterServiceStore: &fakeClusterServiceStore{
			resolved: map[string][]string{"payments/payment-api": {"cluster-42", "cluster-99"}},
		}}
		req := httptest.NewRequest(http.MethodGet, "/api/resolve-cluster?namespace=payments&service=payment-api", nil)
		w := httptest.NewRecorder()

		h.HandleResolveCluster(w, req)

		var resp resolveClusterResponse
		json.NewDecoder(w.Body).Decode(&resp)
		if len(resp.ClusterIDs) != 2 {
			t.Errorf("ClusterIDs: want 2 matches, got %v", resp.ClusterIDs)
		}
	})

	t.Run("no cluster service store configured degrades to an empty list", func(t *testing.T) {
		h := &Handler{}
		req := httptest.NewRequest(http.MethodGet, "/api/resolve-cluster?namespace=payments&service=payment-api", nil)
		w := httptest.NewRecorder()

		h.HandleResolveCluster(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status: want %d, got %d", http.StatusOK, w.Code)
		}
	})
}
