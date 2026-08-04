package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleSyncNamespaces exercises the namespace-sync endpoint's ADR 0027
// behavior: it re-derives from ClusterServiceStore (Discovery's own push
// data, ADR 0022/ROADMAP P18 use case #1) instead of calling out to the
// retired k8fy adapter. Full happy-path coverage (a real seeded
// current_state write) needs a real orchestrator/Postgres backend and is
// exercised at the integration-test tier, not here — this covers the
// request-shape and store-not-configured guard, which don't need one.
func TestHandleSyncNamespaces(t *testing.T) {
	t.Run("cluster service store not configured returns 503", func(t *testing.T) {
		h := &Handler{}
		req := httptest.NewRequest(http.MethodPost, "/admin/sync", nil)
		w := httptest.NewRecorder()

		h.HandleSyncNamespaces(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status: want %d, got %d", http.StatusServiceUnavailable, w.Code)
		}
	})

	t.Run("wrong HTTP method is rejected", func(t *testing.T) {
		h := &Handler{}
		req := httptest.NewRequest(http.MethodDelete, "/admin/sync", nil)
		w := httptest.NewRecorder()

		h.HandleSyncNamespaces(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status: want %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
	})

	t.Run("GET is allowed (CronJob calls it via curl GET fallback)", func(t *testing.T) {
		h := &Handler{}
		req := httptest.NewRequest(http.MethodGet, "/admin/sync", nil)
		w := httptest.NewRecorder()

		h.HandleSyncNamespaces(w, req)

		// Store not configured -> 503, not 405 -- proves GET passed the method check.
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status: want %d, got %d", http.StatusServiceUnavailable, w.Code)
		}
	})
}

// Note: HandleTrackedEntities isn't covered here — every path through it
// (including the early "no kv backend configured" case) calls
// h.orch.GetBackendFactory() first, which panics on a nil *orchestrator.Router
// rather than degrading — a pre-existing characteristic unrelated to ADR
// 0027's adapter-to-Postgres swap (the live-seed fallback this task changed
// is further down the same function). Needs a real orchestrator/Postgres
// backend to test meaningfully; left for the integration-test tier, same as
// noted on HandleSyncNamespaces above.
