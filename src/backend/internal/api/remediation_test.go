package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckRemediationAuth(t *testing.T) {
	t.Run("no token configured allows everything (dev only)", func(t *testing.T) {
		h := &Handler{remediationConfig: RemediationConfig{AuthToken: ""}}
		req := httptest.NewRequest(http.MethodPost, "/admin/remediation/x/approve", nil)
		if !h.checkRemediationAuth(req) {
			t.Fatal("expected auth to pass when no token is configured")
		}
	})

	t.Run("correct bearer token passes", func(t *testing.T) {
		h := &Handler{remediationConfig: RemediationConfig{AuthToken: "s3cret"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/remediation/x/approve", nil)
		req.Header.Set("Authorization", "Bearer s3cret")
		if !h.checkRemediationAuth(req) {
			t.Fatal("expected auth to pass with the correct token")
		}
	})

	t.Run("wrong token fails", func(t *testing.T) {
		h := &Handler{remediationConfig: RemediationConfig{AuthToken: "s3cret"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/remediation/x/approve", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		if h.checkRemediationAuth(req) {
			t.Fatal("expected auth to fail with the wrong token")
		}
	})

	t.Run("missing header fails when token configured", func(t *testing.T) {
		h := &Handler{remediationConfig: RemediationConfig{AuthToken: "s3cret"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/remediation/x/approve", nil)
		if h.checkRemediationAuth(req) {
			t.Fatal("expected auth to fail with no Authorization header")
		}
	})

	t.Run("malformed header (no Bearer prefix) fails", func(t *testing.T) {
		h := &Handler{remediationConfig: RemediationConfig{AuthToken: "s3cret"}}
		req := httptest.NewRequest(http.MethodPost, "/admin/remediation/x/approve", nil)
		req.Header.Set("Authorization", "s3cret")
		if h.checkRemediationAuth(req) {
			t.Fatal("expected auth to fail without the Bearer prefix")
		}
	})
}
