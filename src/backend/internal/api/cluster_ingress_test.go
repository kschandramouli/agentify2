package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pgstore "github.com/chan/agentify/backend/internal/storage/postgres"
)

// fakeClusterIngressStore implements ClusterIngressStore, recording the last
// UpsertClusterIngress call and serving ListClusterIngress from a canned map
// keyed by namespace.
type fakeClusterIngressStore struct {
	lastTenantID  string
	lastClusterID string
	lastEntries   []pgstore.IngressEndpoint
	byNamespace   map[string][]pgstore.IngressEndpoint
	listErr       error
}

func (f *fakeClusterIngressStore) UpsertClusterIngress(ctx context.Context, tenantID, clusterID string, entries []pgstore.IngressEndpoint) error {
	f.lastTenantID = tenantID
	f.lastClusterID = clusterID
	f.lastEntries = entries
	return nil
}

func (f *fakeClusterIngressStore) ListClusterIngress(ctx context.Context, tenantID, namespace string) ([]pgstore.IngressEndpoint, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.byNamespace == nil {
		return []pgstore.IngressEndpoint{}, nil
	}
	return f.byNamespace[namespace], nil
}

// TestHandleClusterIngressUpsert exercises the fleet collector's entry-point
// mapping push (ROADMAP P18 use case #3) — same auth shape as
// HandleClusterInventoryUpsert: an absent or unrecognized credential is
// always rejected since there's no cluster identity to attach entries to
// otherwise.
func TestHandleClusterIngressUpsert(t *testing.T) {
	t.Run("no Authorization header is rejected — no cluster to attach entries to", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{}, clusterIngressStore: &fakeClusterIngressStore{}}
		req := httptest.NewRequest(http.MethodPost, "/api/cluster-ingress", strings.NewReader(`{"entries":[]}`))
		w := httptest.NewRecorder()

		h.HandleClusterIngressUpsert(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status: want %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("unrecognized bearer token is rejected", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{}, clusterIngressStore: &fakeClusterIngressStore{}}
		req := httptest.NewRequest(http.MethodPost, "/api/cluster-ingress", strings.NewReader(`{"entries":[]}`))
		req.Header.Set("Authorization", "Bearer nonexistent-token")
		w := httptest.NewRecorder()

		h.HandleClusterIngressUpsert(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status: want %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("cluster ingress store not configured returns 503", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{
			byToken: map[string]*pgstore.Integration{"real-token": {ID: "cluster-42", TenantID: "tenant-a"}},
		}}
		req := httptest.NewRequest(http.MethodPost, "/api/cluster-ingress", strings.NewReader(`{"entries":[]}`))
		req.Header.Set("Authorization", "Bearer real-token")
		w := httptest.NewRecorder()

		h.HandleClusterIngressUpsert(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status: want %d, got %d", http.StatusServiceUnavailable, w.Code)
		}
	})

	t.Run("valid collector token upserts entries", func(t *testing.T) {
		integStore := &fakeIntegrationStore{
			byToken: map[string]*pgstore.Integration{"real-token": {ID: "cluster-42", TenantID: "tenant-a"}},
		}
		ciStore := &fakeClusterIngressStore{}
		h := &Handler{integrationStore: integStore, clusterIngressStore: ciStore}
		body := `{"entries":[{"namespace":"payments","kind":"ingress","name":"shop-ingress","host":"shop.example.com","backend_service":"storefront"}]}`
		req := httptest.NewRequest(http.MethodPost, "/api/cluster-ingress", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer real-token")
		w := httptest.NewRecorder()

		h.HandleClusterIngressUpsert(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("status: want %d, got %d", http.StatusNoContent, w.Code)
		}
		if ciStore.lastTenantID != "tenant-a" || ciStore.lastClusterID != "cluster-42" {
			t.Errorf("UpsertClusterIngress called with wrong tenant/cluster: %q/%q", ciStore.lastTenantID, ciStore.lastClusterID)
		}
		if len(ciStore.lastEntries) != 1 || ciStore.lastEntries[0].Name != "shop-ingress" {
			t.Errorf("UpsertClusterIngress entries: got %v", ciStore.lastEntries)
		}
	})

	t.Run("malformed JSON is rejected", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{
			byToken: map[string]*pgstore.Integration{"real-token": {ID: "cluster-42", TenantID: "tenant-a"}},
		}, clusterIngressStore: &fakeClusterIngressStore{}}
		req := httptest.NewRequest(http.MethodPost, "/api/cluster-ingress", strings.NewReader(`not json`))
		req.Header.Set("Authorization", "Bearer real-token")
		w := httptest.NewRecorder()

		h.HandleClusterIngressUpsert(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status: want %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("wrong HTTP method is rejected", func(t *testing.T) {
		h := &Handler{clusterIngressStore: &fakeClusterIngressStore{}}
		req := httptest.NewRequest(http.MethodGet, "/api/cluster-ingress", nil)
		w := httptest.NewRecorder()

		h.HandleClusterIngressUpsert(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status: want %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
	})
}

// TestHandleClusterIngressList exercises the read side (store-only surface —
// no agent tool consumes this yet).
func TestHandleClusterIngressList(t *testing.T) {
	t.Run("missing namespace is rejected", func(t *testing.T) {
		h := &Handler{clusterIngressStore: &fakeClusterIngressStore{}}
		req := httptest.NewRequest(http.MethodGet, "/api/cluster-ingress", nil)
		w := httptest.NewRecorder()

		h.HandleClusterIngressList(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status: want %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("store not configured returns empty list, not an error", func(t *testing.T) {
		h := &Handler{}
		req := httptest.NewRequest(http.MethodGet, "/api/cluster-ingress?namespace=payments", nil)
		w := httptest.NewRecorder()

		h.HandleClusterIngressList(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status: want %d, got %d", http.StatusOK, w.Code)
		}
		if !strings.Contains(w.Body.String(), `"entries":[]`) {
			t.Errorf("body: want empty entries, got %s", w.Body.String())
		}
	})

	t.Run("returns matching entries for the namespace", func(t *testing.T) {
		ciStore := &fakeClusterIngressStore{byNamespace: map[string][]pgstore.IngressEndpoint{
			"payments": {{Namespace: "payments", Kind: "ingress", Name: "shop-ingress", Host: "shop.example.com", BackendService: "storefront"}},
		}}
		h := &Handler{clusterIngressStore: ciStore}
		req := httptest.NewRequest(http.MethodGet, "/api/cluster-ingress?namespace=payments", nil)
		w := httptest.NewRecorder()

		h.HandleClusterIngressList(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status: want %d, got %d", http.StatusOK, w.Code)
		}
		if !strings.Contains(w.Body.String(), "shop-ingress") {
			t.Errorf("body: want shop-ingress entry, got %s", w.Body.String())
		}
	})
}
