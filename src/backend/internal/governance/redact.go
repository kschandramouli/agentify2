// Package governance enforces egress data governance: it minimizes the data the
// backend sends toward the agent/model by applying an allowlist (drop everything
// not explicitly needed), with optional identifier pseudonymization.
//
// See context-mesh/policies/data-governance.md and ADR 0007.
package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"regexp"
)

// Redactor applies the egress allowlist to data leaving the backend boundary.
type Redactor struct {
	enabled      bool
	pseudonymize bool
	logger       *slog.Logger

	recordAllow  map[string]struct{} // top-level keys kept on each record
	payloadAllow map[string]struct{} // keys kept inside a record's payload
	identifiers  map[string]struct{} // fields pseudonymized when enabled
}

// NewRedactor builds a Redactor with the default K8fy allowlist.
func NewRedactor(enabled, pseudonymize bool, logger *slog.Logger) *Redactor {
	set := func(keys ...string) map[string]struct{} {
		m := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			m[k] = struct{}{}
		}
		return m
	}
	return &Redactor{
		enabled:      enabled,
		pseudonymize: pseudonymize,
		logger:       logger,
		recordAllow:  set("entity_key", "event_namespace", "type", "timestamp", "source", "payload"),
		payloadAllow: set(
			"pod_id", "namespace", "phase", "ready", "restarts", "reason", "message",
			"service", "endpoints", "ready_endpoints", "ready_ratio", "container",
			"secret", "expires_at", "days_until_expiry", "should_renew",
		),
		identifiers: set("pod_id", "namespace", "service", "secret", "entity_key"),
	}
}

// RedactPodData redacts the /api/query shape: map[podID] -> {data: []rows, ...}.
// Returns a new map; the input is left untouched (Tier-1 still needs the raw data).
func (r *Redactor) RedactPodData(podData map[string]interface{}) map[string]interface{} {
	if !r.enabled || podData == nil {
		return podData
	}
	out := make(map[string]interface{}, len(podData))
	for podID, v := range podData {
		entry, ok := v.(map[string]interface{})
		if !ok {
			out[podID] = v
			continue
		}
		cp := make(map[string]interface{}, len(entry))
		for k, val := range entry {
			cp[k] = val
		}
		if rows, ok := entry["data"].([]map[string]interface{}); ok {
			cp["data"] = r.redactRows(rows)
		}
		out[podID] = cp
	}
	return out
}

// RedactFetch redacts the /api/agent/fetch shape: map[podID] -> []rows.
func (r *Redactor) RedactFetch(data map[string]interface{}) map[string]interface{} {
	if !r.enabled || data == nil {
		return data
	}
	out := make(map[string]interface{}, len(data))
	for podID, v := range data {
		if rows, ok := v.([]map[string]interface{}); ok {
			out[podID] = r.redactRows(rows)
		} else {
			out[podID] = v
		}
	}
	return out
}

func (r *Redactor) redactRows(rows []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.redactRecord(row))
	}
	return out
}

func (r *Redactor) redactRecord(row map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range row {
		if _, ok := r.recordAllow[k]; !ok {
			continue // drop anything not explicitly allowed
		}
		if k == "payload" {
			if p, ok := v.(map[string]interface{}); ok {
				out[k] = r.redactPayload(p)
				continue
			}
		}
		out[k] = r.maybePseudonymize(k, v)
	}
	return out
}

func (r *Redactor) redactPayload(p map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range p {
		if _, ok := r.payloadAllow[k]; !ok {
			continue
		}
		out[k] = r.maybePseudonymize(k, v)
	}
	return out
}

func (r *Redactor) maybePseudonymize(key string, v interface{}) interface{} {
	if !r.pseudonymize {
		return v
	}
	if _, ok := r.identifiers[key]; !ok {
		return v
	}
	if s, ok := v.(string); ok && s != "" {
		return pseudonym(s)
	}
	return v
}

// pseudonym returns a stable, non-reversible token for an identifier value.
func pseudonym(s string) string {
	h := sha256.Sum256([]byte(s))
	return "id_" + hex.EncodeToString(h[:])[:10]
}

// --- Best-effort text scrubbing for freeform logs (ADR 0014) ---
//
// IMPORTANT: this is a DENYLIST, not the allowlist guarantee the structured
// redactor above provides. It masks known secret shapes in log text but WILL miss
// novel ones. It is the reason logs are fetched on-demand and never persisted
// (ADR 0014): the blast radius of a miss is a transient leak to the model, not a
// permanent one in storage.

// maxLogChars caps scrubbed log text so a tail can't blow up token cost.
const maxLogChars = 16384

var logScrubbers = []*regexp.Regexp{
	// Authorization: Bearer <token> / "bearer <token>"
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(bearer\s+)?[A-Za-z0-9._\-]{12,}`),
	// AWS access key IDs
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// JWT-ish: three base64url segments
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`),
	// key=secret / "password": "..." style assignments
	regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)("?\s*[:=]\s*"?)[^\s"',;}]+`),
	// connection-string password: proto://user:pass@host  → mask the pass
	regexp.MustCompile(`(://[^:/\s]+:)[^@/\s]+(@)`),
	// emails
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	// long hex / base64 blobs (likely keys/hashes)
	regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`),
	regexp.MustCompile(`\b[A-Za-z0-9+/]{40,}={0,2}\b`),
}

// replacements pairs each scrubber with how to rewrite a match. Patterns that have
// a "prefix to keep" use a capture-group template; the rest mask wholesale.
var logReplacements = []string{
	`${1}${2}***`, // authorization
	`***`,         // aws key
	`***`,         // jwt
	`${1}${2}***`, // key=secret (keep field name + delimiter)
	`${1}***${2}`, // conn-string password
	`***`,         // email
	`***`,         // hex blob
	`***`,         // base64 blob
}

// RedactText masks common secret shapes in freeform text (logs) and truncates it.
// A no-op when redaction is disabled. See ADR 0014 for the (weaker) guarantee.
func (r *Redactor) RedactText(s string) string {
	if !r.enabled {
		return s
	}
	for i, re := range logScrubbers {
		s = re.ReplaceAllString(s, logReplacements[i])
	}
	if len(s) > maxLogChars {
		s = s[:maxLogChars] + "\n…[truncated]"
	}
	return s
}
