package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Alert is a single investigation result destined for the notification channel
// (spec 009). It carries only operator-facing, already-redacted content.
type Alert struct {
	Namespace string
	Severity  string // info | warning | critical (from the agent) or "resolved"
	Reasons   []string
	Summary   string
	Cause     string
	Actions   []string
	Sources   []string
	TraceID   string
	Resolved  bool
	// ProposalID references a pending remediation proposal (ADR 0020) created
	// alongside this alert. Empty when remediation is disabled or proposing
	// failed — the alert is still read-only in that case.
	ProposalID string
}

// Notifier sends an alert to an outbound channel. Implemented by WebhookNotifier;
// an interface so the investigator can be tested without real egress.
type Notifier interface {
	Send(ctx context.Context, a Alert) error
}

// WebhookNotifier posts a Slack-compatible {"text": …} payload to a webhook URL.
type WebhookNotifier struct {
	url    string
	client *http.Client
}

// NewWebhookNotifier creates a notifier. An empty url makes Send a no-op.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{url: url, client: &http.Client{Timeout: 10 * time.Second}}
}

// Send posts the formatted alert. No-op when no URL is configured.
func (n *WebhookNotifier) Send(ctx context.Context, a Alert) error {
	if n.url == "" {
		return nil
	}
	body, err := json.Marshal(map[string]string{"text": formatAlertText(a)})
	if err != nil {
		return fmt.Errorf("marshal alert: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build alert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("post alert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

// formatAlertText renders a human-readable, single-string summary (Slack `text`).
func formatAlertText(a Alert) string {
	var b strings.Builder
	if a.Resolved {
		fmt.Fprintf(&b, "✅ RESOLVED — namespace %s recovered", a.Namespace)
		if a.TraceID != "" {
			fmt.Fprintf(&b, "\ntrace_id: %s", a.TraceID)
		}
		return b.String()
	}

	sev := strings.ToUpper(a.Severity)
	if sev == "" {
		sev = "ALERT"
	}
	fmt.Fprintf(&b, "🚨 %s — namespace %s needs attention", sev, a.Namespace)
	if len(a.Reasons) > 0 {
		fmt.Fprintf(&b, "\nTrigger: %s", strings.Join(a.Reasons, "; "))
	}
	if a.Summary != "" {
		fmt.Fprintf(&b, "\n\n%s", a.Summary)
	}
	if a.Cause != "" {
		fmt.Fprintf(&b, "\n\nLikely cause: %s", a.Cause)
	}
	for _, act := range a.Actions {
		fmt.Fprintf(&b, "\n• %s", act)
	}
	if len(a.Sources) > 0 {
		fmt.Fprintf(&b, "\n\nSources: %s", strings.Join(a.Sources, ", "))
	}
	if a.TraceID != "" {
		fmt.Fprintf(&b, "\ntrace_id: %s", a.TraceID)
	}
	if a.ProposalID != "" {
		fmt.Fprintf(&b, "\n\n⏸ Remediation proposal #%s is pending review in the Admin Console — no action taken automatically.", a.ProposalID)
	} else {
		b.WriteString("\n(read-only diagnosis — no action taken)")
	}
	return b.String()
}
