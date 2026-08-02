"""log_redaction.py — best-effort secret scrubbing for raw log text.

Copied verbatim from src/agent/k8fy/log_redaction.py (ADR 0022 Decision #6:
"redaction runs at the collector, before anything leaves the cluster" — this
package deliberately does not import from src/agent, which carries a much
heavier dependency set; see the "Layout" section of the agentify-discovery
plan). Keep this in sync by hand if the original ever changes shape.
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
