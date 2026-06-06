package governance

import (
	"log/slog"
	"strings"
	"testing"
)

func sampleFetch() map[string]interface{} {
	return map[string]interface{}{
		"k8fy.live-state.prod": []map[string]interface{}{
			{
				"id":              "evt-1",
				"event_id":        "evt-1",
				"entity_key":      "payment-svc-abc",
				"event_namespace": "k8fy.live-state",
				"type":            "pod_modified",
				"timestamp":       "2026-06-01T00:00:00Z",
				"source":          "kubernetes-api",
				"payload": map[string]interface{}{
					"pod_id":    "payment-svc-abc",
					"namespace": "prod",
					"phase":     "Running",
					"ready":     false,
					"restarts":  float64(7),
					"reason":    "CrashLoopBackOff",
					// sensitive / non-allowlisted fields that must be dropped:
					"annotations": map[string]interface{}{"vault-token": "s.SECRET"},
					"env":         []string{"DB_PASSWORD=hunter2"},
				},
			},
		},
	}
}

func TestRedactDropsNonAllowlisted(t *testing.T) {
	r := NewRedactor(true, false, slog.Default())
	out := r.RedactFetch(sampleFetch())

	rows := out["k8fy.live-state.prod"].([]map[string]interface{})
	rec := rows[0]

	// dropped top-level keys
	for _, k := range []string{"id", "event_id"} {
		if _, ok := rec[k]; ok {
			t.Errorf("expected top-level %q to be dropped", k)
		}
	}
	// kept top-level keys
	for _, k := range []string{"entity_key", "event_namespace", "type", "payload"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("expected top-level %q to be kept", k)
		}
	}

	payload := rec["payload"].(map[string]interface{})
	// sensitive fields dropped
	for _, k := range []string{"annotations", "env"} {
		if _, ok := payload[k]; ok {
			t.Errorf("expected sensitive payload field %q to be dropped", k)
		}
	}
	// reasoning fields kept
	for _, k := range []string{"pod_id", "phase", "ready", "restarts", "reason"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("expected payload field %q to be kept", k)
		}
	}
}

func TestPseudonymizeIdentifiers(t *testing.T) {
	r := NewRedactor(true, true, slog.Default())
	out := r.RedactFetch(sampleFetch())
	rec := out["k8fy.live-state.prod"].([]map[string]interface{})[0]
	payload := rec["payload"].(map[string]interface{})

	if got := payload["pod_id"].(string); got == "payment-svc-abc" {
		t.Error("pod_id should be pseudonymized when enabled")
	} else if len(got) < 4 || got[:3] != "id_" {
		t.Errorf("pseudonym should be id_<hash>, got %q", got)
	}
	// non-identifier fields must NOT be altered
	if payload["phase"].(string) != "Running" {
		t.Error("non-identifier field phase must not be pseudonymized")
	}
}

func TestRedactDisabledIsPassthrough(t *testing.T) {
	r := NewRedactor(false, false, slog.Default())
	in := sampleFetch()
	out := r.RedactFetch(in)
	rec := out["k8fy.live-state.prod"].([]map[string]interface{})[0]
	if _, ok := rec["payload"].(map[string]interface{})["env"]; !ok {
		t.Error("disabled redactor should pass data through unchanged")
	}
}

func TestRedactTextScrubsSecrets(t *testing.T) {
	r := NewRedactor(true, false, slog.Default())
	in := `level=error connecting db
postgres://app:hunter2@db.internal:5432/payments failed
Authorization: Bearer abcdef0123456789ABCDEF
AWS_KEY=AKIAIOSFODNN7EXAMPLE password=supersecret123
contact ops@example.com token: eyJhbGciOi.JzdWIiOiI.SflKxwRJSM
digest 0123456789abcdef0123456789abcdef`

	out := r.RedactText(in)

	for _, s := range []string{"hunter2", "supersecret123", "abcdef0123456789ABCDEF", "AKIAIOSFODNN7EXAMPLE", "ops@example.com", "eyJhbGciOi.JzdWIiOiI.SflKxwRJSM", "0123456789abcdef0123456789abcdef"} {
		if strings.Contains(out, s) {
			t.Errorf("RedactText leaked %q\n--- output ---\n%s", s, out)
		}
	}
	// Non-secret context must survive so the log stays useful.
	if !strings.Contains(out, "connecting db") || !strings.Contains(out, "failed") {
		t.Errorf("RedactText over-scrubbed useful context:\n%s", out)
	}
}

func TestRedactTextTruncates(t *testing.T) {
	r := NewRedactor(true, false, slog.Default())
	out := r.RedactText(strings.Repeat("x", maxLogChars+500))
	if len(out) > maxLogChars+len("\n…[truncated]") {
		t.Errorf("RedactText did not truncate: len=%d", len(out))
	}
}

func TestRedactTextDisabledIsPassthrough(t *testing.T) {
	r := NewRedactor(false, false, slog.Default())
	in := "password=hunter2"
	if got := r.RedactText(in); got != in {
		t.Errorf("disabled RedactText should pass through, got %q", got)
	}
}
