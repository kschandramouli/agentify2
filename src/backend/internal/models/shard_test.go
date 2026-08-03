package models

import "testing"

func TestPodID(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{"namespace-sharded, no cluster (today's shape, unchanged)", []string{"k8fy.live-state", "", "payments"}, "k8fy.live-state.payments"},
		{"namespace-sharded, with cluster", []string{"k8fy.live-state", "cluster-42", "payments"}, "k8fy.live-state.cluster-42.payments"},
		{"single-global pod, no cluster (today's shape, unchanged)", []string{"k8fy.certificates", ""}, "k8fy.certificates"},
		{"single-global pod, with cluster", []string{"k8fy.certificates", "cluster-42"}, "k8fy.certificates.cluster-42"},
		{"all empty", []string{"", ""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PodID(tt.parts...); got != tt.want {
				t.Errorf("PodID(%v) = %q, want %q", tt.parts, got, tt.want)
			}
		})
	}
}
