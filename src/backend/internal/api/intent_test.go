package api

import "testing"

// TestInferIntent_DiagnoseWinsFirst locks spec 005's routing fix: diagnostic
// phrasing must resolve to "diagnose" (Tier-2 fan-out) even when it also contains
// health/cert keywords, so it never drops into the single-signal Tier-1 path.
func TestInferIntent_DiagnoseWinsFirst(t *testing.T) {
	cases := []struct {
		question string
		want     string
	}{
		{"why is payment unhealthy?", "diagnose"},
		{"what's wrong with the checkout service?", "diagnose"},
		{"root cause of the api outage", "diagnose"},
		{"investigate the cert errors", "diagnose"},
		{"diagnose payment", "diagnose"},
		{"the service is broken", "diagnose"},
		// Plain single-signal questions stay on their fast paths.
		{"is payment healthy?", "health_check"},
		{"when does the cert expire?", "cert_check"},
		{"show cpu metrics", "metrics_query"},
		{"list the pods", "general_query"},
	}
	for _, c := range cases {
		if got := inferIntent(c.question); got != c.want {
			t.Errorf("inferIntent(%q) = %q, want %q", c.question, got, c.want)
		}
	}
}
