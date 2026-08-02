package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	pgstore "github.com/chan/agentify/backend/internal/storage/postgres"
)

// fakeIntegrationStore implements IntegrationStore with just enough behavior
// to exercise resolveTenantContext — every other method panics if called,
// since none of these tests should reach them.
type fakeIntegrationStore struct {
	byToken map[string]*pgstore.Integration
}

func (f *fakeIntegrationStore) GetIntegrationByCollectorToken(ctx context.Context, token string) (*pgstore.Integration, error) {
	if in, ok := f.byToken[token]; ok {
		return in, nil
	}
	return nil, sql.ErrNoRows
}

func (f *fakeIntegrationStore) ListIntegrations(ctx context.Context) ([]pgstore.Integration, error) {
	panic("not used by resolveTenantContext tests")
}
func (f *fakeIntegrationStore) GetIntegration(ctx context.Context, id string) (*pgstore.Integration, error) {
	panic("not used by resolveTenantContext tests")
}
func (f *fakeIntegrationStore) CreateIntegration(ctx context.Context, in *pgstore.Integration) error {
	panic("not used by resolveTenantContext tests")
}
func (f *fakeIntegrationStore) UpdateIntegration(ctx context.Context, in *pgstore.Integration) error {
	panic("not used by resolveTenantContext tests")
}
func (f *fakeIntegrationStore) DeleteIntegration(ctx context.Context, id string) error {
	panic("not used by resolveTenantContext tests")
}

func TestResolveTenantContext(t *testing.T) {
	t.Run("no Authorization header defaults to DefaultTenantID, no cluster", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{}}
		req := httptest.NewRequest(http.MethodGet, "/api/service-dependencies?namespace=payments", nil)

		tenantID, clusterID, err := h.resolveTenantContext(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tenantID != pgstore.DefaultTenantID {
			t.Errorf("tenantID: want %q, got %q", pgstore.DefaultTenantID, tenantID)
		}
		if clusterID != "" {
			t.Errorf("clusterID: want empty, got %q", clusterID)
		}
	})

	t.Run("unrecognized bearer token is rejected, not defaulted", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{}}
		req := httptest.NewRequest(http.MethodPost, "/api/service-dependencies", nil)
		req.Header.Set("Authorization", "Bearer nonexistent-token")

		_, _, err := h.resolveTenantContext(req)
		if !errors.Is(err, errInvalidCredential) {
			t.Fatalf("want errInvalidCredential, got %v", err)
		}
	})

	t.Run("valid collector token resolves the matching Integration's tenant and cluster", func(t *testing.T) {
		h := &Handler{integrationStore: &fakeIntegrationStore{
			byToken: map[string]*pgstore.Integration{
				"real-token": {ID: "cluster-42", TenantID: "tenant-a"},
			},
		}}
		req := httptest.NewRequest(http.MethodPost, "/api/service-dependencies", nil)
		req.Header.Set("Authorization", "Bearer real-token")

		tenantID, clusterID, err := h.resolveTenantContext(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tenantID != "tenant-a" {
			t.Errorf("tenantID: want %q, got %q", "tenant-a", tenantID)
		}
		if clusterID != "cluster-42" {
			t.Errorf("clusterID: want %q, got %q", "cluster-42", clusterID)
		}
	})

	t.Run("no integration store configured rejects any presented credential", func(t *testing.T) {
		h := &Handler{integrationStore: nil}
		req := httptest.NewRequest(http.MethodPost, "/api/service-dependencies", nil)
		req.Header.Set("Authorization", "Bearer whatever")

		_, _, err := h.resolveTenantContext(req)
		if !errors.Is(err, errInvalidCredential) {
			t.Fatalf("want errInvalidCredential, got %v", err)
		}
	})
}
