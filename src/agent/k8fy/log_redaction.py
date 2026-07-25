"""log_redaction.py — best-effort secret scrubbing for raw log text.

Shared by every code path that reads log text the Go backend's egress
redactor (governance.RedactText, src/backend/internal/governance/redact.go)
never sees: `live_diagnostics.py` (talks to the K8s API directly) and
`log_platform.py` (talks to Athena directly). Both bypass the backend, so
this is the only scrubbing that text ever gets. Mirrors RedactText's patterns
so the guarantee is the same regardless of which path answered.
"""

import re

_MAX_LOG_CHARS = 16384

_LOG_SCRUBBERS = [
    (re.compile(r"(?i)(authorization\s*[:=]\s*)(bearer\s+)?[A-Za-z0-9._\-]{12,}"), r"\1\2***"),
    (re.compile(r"AKIA[0-9A-Z]{16}"), "***"),
    (re.compile(r"eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+"), "***"),
    (re.compile(r"(?i)(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)(\"?\s*[:=]\s*\"?)[^\s\"',;}]+"), r"\1\2***"),
    (re.compile(r"(://[^:/\s]+:)[^@/\s]+(@)"), r"\1***\2"),
    (re.compile(r"[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}"), "***"),
    (re.compile(r"\b[A-Fa-f0-9]{32,}\b"), "***"),
    (re.compile(r"\b[A-Za-z0-9+/]{40,}={0,2}\b"), "***"),
]


def redact_log_text(s: str) -> str:
    for pattern, replacement in _LOG_SCRUBBERS:
        s = pattern.sub(replacement, s)
    if len(s) > _MAX_LOG_CHARS:
        s = s[:_MAX_LOG_CHARS] + "\n…[truncated]"
    return s
