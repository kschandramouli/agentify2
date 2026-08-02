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

	t.Run("multi-tenancy migration: existing insert paths default tenant_id, leave cluster_id empty", func(t *testing.T) {
		// ADR 0022, phase 1 (schema only): CreateIntegration doesn't reference
		// tenant_id/cluster_id at all, so Postgres must apply the column
		// default/NULL on its own — this is the guarantee the whole
		// schema-only phase rests on (no existing INSERT path changed).
		in := &Integration{
			ID:         uuid.New().String(),
			Name:       "tenancy-test",
			AdapterURL: "http://example.invalid",
			Namespaces: []string{},
			Status:     "inactive",
		}
		if err := client.CreateIntegration(ctx, in); err != nil {
			t.Fatalf("create integration: %v", err)
		}
		got, err := client.GetIntegration(ctx, in.ID)
		if err != nil {
			t.Fatalf("get integration: %v", err)
		}
		if got.TenantID != DefaultTenantID {
			t.Errorf("tenant_id: want default %q, got %q", DefaultTenantID, got.TenantID)
		}
		if got.ClusterID != "" {
			t.Errorf("cluster_id: want empty (NULL), got %q", got.ClusterID)
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

// TestRemediationProposals covers the propose→approve/reject lifecycle (ADR
// 0020): create, list/filter by status, and — most importantly — that the
// decide step is idempotent under the WHERE status='pending' guard so a
// duplicate approve/reject (double click, webhook retry) never re-decides or
// re-executes an already-decided proposal.
func TestRemediationProposals(t *testing.T) {
	client := startEmbedded(t)
	ctx := context.Background()

	p := &RemediationProposal{
		ID:             uuid.New().String(),
		TraceID:        "trace-1",
		UseCase:        "incident_responder",
		Namespace:      "payments",
		Service:        "payment-worker",
		ProposedAction: "restart_deployment",
		ActionParams:   map[string]interface{}{"deployment": "payment-worker"},
		Analysis:       map[string]interface{}{"reasoning": "OOMKilled 3x", "confidence": 0.8},
		ExpiresAt:      time.Now().Add(30 * time.Minute),
	}
	if err := client.CreateRemediationProposal(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("get round-trips fields", func(t *testing.T) {
		got, err := client.GetRemediationProposal(ctx, p.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != "pending" {
			t.Errorf("want status=pending, got %q", got.Status)
		}
		if got.ActionParams["deployment"] != "payment-worker" {
			t.Errorf("action_params not round-tripped: %v", got.ActionParams)
		}
		if got.Analysis["reasoning"] != "OOMKilled 3x" {
			t.Errorf("analysis not round-tripped: %v", got.Analysis)
		}
	})

	t.Run("list filters by status", func(t *testing.T) {
		pending, err := client.ListRemediationProposals(ctx, "pending", 100)
		if err != nil || len(pending) != 1 {
			t.Fatalf("list pending: err=%v rows=%d", err, len(pending))
		}
		approved, err := client.ListRemediationProposals(ctx, "approved", 100)
		if err != nil || len(approved) != 0 {
			t.Fatalf("list approved: err=%v rows=%d (want 0)", err, len(approved))
		}
	})

	t.Run("decide is idempotent — second decision is a no-op", func(t *testing.T) {
		ok, err := client.DecideRemediationProposal(ctx, p.ID, "approved", "test-actor")
		if err != nil || !ok {
			t.Fatalf("first decide: ok=%v err=%v (want ok=true)", ok, err)
		}
		ok2, err := client.DecideRemediationProposal(ctx, p.ID, "rejected", "someone-else")
		if err != nil {
			t.Fatalf("second decide errored: %v", err)
		}
		if ok2 {
			t.Fatal("second decide should be a no-op (ok=false) — proposal was already decided")
		}
		got, err := client.GetRemediationProposal(ctx, p.ID)
		if err != nil {
			t.Fatalf("get after decide: %v", err)
		}
		if got.Status != "approved" {
			t.Errorf("status should remain 'approved' from the first decision, got %q", got.Status)
		}
		if got.DecidedBy != "test-actor" {
			t.Errorf("decided_by should remain from the first decision, got %q", got.DecidedBy)
		}
	})

	t.Run("complete records execution outcome", func(t *testing.T) {
		if err := client.CompleteRemediationProposal(ctx, p.ID, "executed",
			map[string]interface{}{"status": "restarted"}, ""); err != nil {
			t.Fatalf("complete: %v", err)
		}
		got, err := client.GetRemediationProposal(ctx, p.ID)
		if err != nil {
			t.Fatalf("get after complete: %v", err)
		}
		if got.Status != "executed" {
			t.Errorf("want status=executed, got %q", got.Status)
		}
		if got.Result["status"] != "restarted" {
			t.Errorf("result not round-tripped: %v", got.Result)
		}
	})

	t.Run("ProposalExistsForEvent dedupes DeploymentGuardian's sweep", func(t *testing.T) {
		eventID := uuid.New().String()
		exists, err := client.ProposalExistsForEvent(ctx, eventID)
		if err != nil || exists {
			t.Fatalf("expected no proposal yet: exists=%v err=%v", exists, err)
		}
		dgp := &RemediationProposal{
			ID: uuid.New().String(), UseCase: "deployment_guardian",
			Namespace: "payments", Service: "payment-api", ProposedAction: "rollback_deployment",
			SourceEventID: eventID, ExpiresAt: time.Now().Add(30 * time.Minute),
		}
		if err := client.CreateRemediationProposal(ctx, dgp); err != nil {
			t.Fatalf("create: %v", err)
		}
		exists, err = client.ProposalExistsForEvent(ctx, eventID)
		if err != nil || !exists {
			t.Fatalf("expected proposal to exist: exists=%v err=%v", exists, err)
		}
	})
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
