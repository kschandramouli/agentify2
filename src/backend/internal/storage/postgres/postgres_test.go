package postgres

import (
	"context"
	"log/slog"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"
)

// startEmbedded boots a throwaway Postgres for the test, or skips if the binary
// can't be downloaded/started (e.g. no network / unsupported platform).
func startEmbedded(t *testing.T) *Client {
	t.Helper()
	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(54329).
			Database("agentify_test"),
	)
	if err := pg.Start(); err != nil {
		t.Skipf("embedded postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	// Embedded-postgres is already running on port 54329 at this point, so a
	// short context is fine — no retry delay expected.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := NewClient(
		ctx,
		"host=localhost port=54329 user=postgres password=postgres dbname=agentify_test sslmode=disable",
		slog.New(slog.NewTextHandler(nopWriter{}, nil)),
	)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestPostgresStores(t *testing.T) {
	client := startEmbedded(t)
	ctx := context.Background()

	t.Run("current_state upsert latest-wins + scan + point-lookup", func(t *testing.T) {
		cs := client.CurrentStateStore()
		pod := "k8fy.live-state.prod"

		// pod-a: first crashing, then overwritten healthy (latest-wins).
		mustStore(t, cs, pod, "pod-a", map[string]interface{}{"pod_id": "pod-a", "ready": false, "restarts": float64(7), "reason": "CrashLoopBackOff"})
		mustStore(t, cs, pod, "pod-a", map[string]interface{}{"pod_id": "pod-a", "ready": true, "restarts": float64(0)})
		mustStore(t, cs, pod, "pod-b", map[string]interface{}{"pod_id": "pod-b", "ready": true, "restarts": float64(0)})

		// scan: 2 distinct entities (pod-a deduped by upsert).
		rows, err := cs.Query(ctx, pod, map[string]interface{}{})
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("scan: want 2 entities, got %d", len(rows))
		}

		// point lookup pod-a: payload must be a map and reflect the latest write.
		one, err := cs.Query(ctx, pod, map[string]interface{}{"key": "pod-a"})
		if err != nil || len(one) != 1 {
			t.Fatalf("point lookup: err=%v rows=%d", err, len(one))
		}
		payload, ok := one[0]["payload"].(map[string]interface{})
		if !ok {
			t.Fatalf("payload should decode to a map, got %T", one[0]["payload"])
		}
		if payload["ready"] != true {
			t.Errorf("latest-wins failed: want ready=true, got %v", payload["ready"])
		}
	})

	t.Run("events append + payload decodes to map", func(t *testing.T) {
		pod := "k8fy.events"
		for i := 0; i < 2; i++ {
			_, err := client.Store(ctx, pod, map[string]interface{}{
				"id":              uuid.New().String(),
				"event_namespace": "k8fy.events",
				"type":            "pod_restart",
				"timestamp":       "2026-06-02T00:00:00Z",
				"payload":         map[string]interface{}{"reason": "CrashLoopBackOff"},
			})
			if err != nil {
				t.Fatalf("event store: %v", err)
			}
		}
		rows, err := client.Query(ctx, pod, nil)
		if err != nil || len(rows) != 2 {
			t.Fatalf("event query: err=%v rows=%d", err, len(rows))
		}
		if _, ok := rows[0]["payload"].(map[string]interface{}); !ok {
			t.Errorf("event payload should decode to a map, got %T", rows[0]["payload"])
		}
	})
}

func TestEventsWindowedQuery(t *testing.T) {
	client := startEmbedded(t)
	ctx := context.Background()
	pod := "k8fy.metrics"

	// A rising restart series for pod-x, plus one unrelated sample for pod-y.
	samples := []struct {
		ts       string
		podID    string
		restarts float64
	}{
		{"2026-06-05T14:00:00Z", "pod-x", 0},
		{"2026-06-05T14:10:00Z", "pod-x", 3},
		{"2026-06-05T14:20:00Z", "pod-x", 11},
		{"2026-06-05T14:30:00Z", "pod-x", 17},
		{"2026-06-05T14:15:00Z", "pod-y", 0},
	}
	for _, s := range samples {
		if _, err := client.Store(ctx, pod, map[string]interface{}{
			"id":              uuid.New().String(),
			"event_namespace": "k8fy.metrics",
			"type":            "pod_metrics",
			"timestamp":       s.ts,
			"payload":         map[string]interface{}{"pod_id": s.podID, "restarts": s.restarts},
		}); err != nil {
			t.Fatalf("store sample: %v", err)
		}
	}

	// Window 14:05–14:25, entity pod-x, chronological: expect the 14:10 and 14:20
	// samples only (excludes 14:00 boundary-before, 14:30 after, and pod-y).
	rows, err := client.Query(ctx, pod, map[string]interface{}{
		"since": "2026-06-05T14:05:00Z",
		"until": "2026-06-05T14:25:00Z",
		"entity": "pod-x",
		"order": "asc",
	})
	if err != nil {
		t.Fatalf("windowed query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 windowed samples, got %d", len(rows))
	}
	// Chronological order + entity filter held.
	first := rows[0]["payload"].(map[string]interface{})
	second := rows[1]["payload"].(map[string]interface{})
	if first["restarts"] != float64(3) || second["restarts"] != float64(11) {
		t.Errorf("asc order/filter wrong: got %v then %v", first["restarts"], second["restarts"])
	}

	// Entity filter alone for pod-x: all 4 samples, recent-first by default.
	all, err := client.Query(ctx, pod, map[string]interface{}{"entity": "pod-x"})
	if err != nil || len(all) != 4 {
		t.Fatalf("entity query: err=%v rows=%d (want 4)", err, len(all))
	}
	if newest := all[0]["payload"].(map[string]interface{}); newest["restarts"] != float64(17) {
		t.Errorf("default order should be recent-first; got newest restarts=%v", newest["restarts"])
	}

	// limit clamps result count.
	lim, err := client.Query(ctx, pod, map[string]interface{}{"entity": "pod-x", "limit": float64(2)})
	if err != nil || len(lim) != 2 {
		t.Fatalf("limit query: err=%v rows=%d (want 2)", err, len(lim))
	}
}

func TestPurgeOlderThan(t *testing.T) {
	client := startEmbedded(t)
	ctx := context.Background()
	pod := "k8fy.metrics"

	// Use time.Now()-based timestamps so the test stays valid regardless of
	// when it runs. The per-pod retention window for k8fy.metrics is 7 days,
	// so "recent" must be within the last 7 days.
	old1   := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339)
	old2   := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-1 * 24 * time.Hour).UTC().Format(time.RFC3339)
	cutoff := time.Now().Add(-8 * 24 * time.Hour) // 8 days ago — between old and recent

	store := func(ts string) {
		if _, err := client.Store(ctx, pod, map[string]interface{}{
			"id":              uuid.New().String(),
			"event_namespace": "k8fy.metrics",
			"type":            "pod_metrics",
			"timestamp":       ts,
			"payload":         map[string]interface{}{"pod_id": "p", "restarts": float64(1)},
		}); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	store(old1)   // 60 days ago — deleted by per-pod 7-day window
	store(old2)   // 30 days ago — deleted by per-pod 7-day window
	store(recent) // yesterday  — kept (within 7-day window)

	n, err := client.PurgeOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("purged %d rows, want 2", n)
	}
	rows, err := client.Query(ctx, pod, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("after purge: err=%v rows=%d (want 1)", err, len(rows))
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return ts
}

func mustStore(t *testing.T, cs *CurrentState, pod, entity string, payload map[string]interface{}) {
	t.Helper()
	_, err := cs.Store(context.Background(), pod, map[string]interface{}{
		"entity_key":      entity,
		"event_namespace": "k8fy.live-state",
		"type":            "pod_modified",
		"source":          "kubernetes-api",
		"payload":         payload,
	})
	if err != nil {
		t.Fatalf("store %s: %v", entity, err)
	}
}
