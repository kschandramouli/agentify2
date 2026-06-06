package evaluator

import "testing"

func TestPodHealth(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]interface{}
		want    string
	}{
		{"healthy", map[string]interface{}{"phase": "Running", "ready": true, "restarts": float64(0)}, StatusHealthy},
		{"crashloop", map[string]interface{}{"phase": "Running", "ready": false, "restarts": float64(7), "reason": "CrashLoopBackOff"}, StatusUnhealthy},
		{"not-ready", map[string]interface{}{"phase": "Running", "ready": false, "restarts": float64(0)}, StatusDegraded},
		{"too-many-restarts", map[string]interface{}{"phase": "Running", "ready": true, "restarts": float64(3)}, StatusDegraded},
		{"failed", map[string]interface{}{"phase": "Failed"}, StatusUnhealthy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := PodHealth(c.payload)
			if got != c.want {
				t.Errorf("PodHealth(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestServiceStatus(t *testing.T) {
	// 1 of 2 healthy = 50% < 75% -> degraded (the live-test scenario).
	results := []PodResult{
		{Entity: "a", Status: StatusHealthy},
		{Entity: "b", Status: StatusUnhealthy},
	}
	status, healthy, ratio := ServiceStatus(results)
	if status != StatusDegraded || healthy != 1 || ratio != 0.5 {
		t.Errorf("ServiceStatus = (%q, %d, %.2f), want (degraded, 1, 0.50)", status, healthy, ratio)
	}

	if s, _, _ := ServiceStatus(nil); s != StatusUnknown {
		t.Errorf("empty ServiceStatus = %q, want unknown", s)
	}
	allHealthy := []PodResult{{Status: StatusHealthy}, {Status: StatusHealthy}}
	if s, _, _ := ServiceStatus(allHealthy); s != StatusHealthy {
		t.Errorf("all-healthy ServiceStatus = %q, want healthy", s)
	}
	noneHealthy := []PodResult{{Status: StatusDegraded}, {Status: StatusUnhealthy}}
	if s, _, _ := ServiceStatus(noneHealthy); s != StatusUnhealthy {
		t.Errorf("none-healthy ServiceStatus = %q, want unhealthy", s)
	}
}

func TestCertRenewal(t *testing.T) {
	if r, _, _ := CertRenewal(map[string]interface{}{"days_until_expiry": float64(10)}); !r {
		t.Error("cert expiring in 10 days should renew")
	}
	if r, _, _ := CertRenewal(map[string]interface{}{"days_until_expiry": float64(90)}); r {
		t.Error("cert valid 90 days should not renew")
	}
	if r, _, _ := CertRenewal(map[string]interface{}{"days_until_expiry": float64(-1)}); !r {
		t.Error("expired cert should renew")
	}
}
