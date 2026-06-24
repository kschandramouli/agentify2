"""One-time script: push all K8fy skill prompts into Langfuse.

Run once after setting credentials:

    export LANGFUSE_PUBLIC_KEY=pk-lf-...
    export LANGFUSE_SECRET_KEY=sk-lf-...
    export LANGFUSE_BASE_URL=https://cloud.langfuse.com   # or your self-hosted URL

    cd src/agent
    python scripts/migrate_prompts_to_langfuse.py

Idempotent: running it again creates a new version (Langfuse auto-increments)
without breaking the service. The "production" label is always moved to the
latest version created by this script.

After running, verify in the Langfuse UI:
  Prompts → filter by "k8fy/" → each should show version 1 with label "production".
"""

import os
import sys

# Ensure the agent src root is on the path so we can import local modules.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from langfuse import Langfuse  # noqa: E402 — after sys.path manipulation

from k8fy.prompts import (  # noqa: E402
    CERT_AUDIT_PROMPT,
    CHANGE_HISTORY_PROMPT,
    DIAGNOSE_PROMPT,
    HEALTH_SKILL_PROMPT,
    RESTART_TREND_PROMPT,
    SYSTEM_PROMPT,
)

PROMPTS = [
    ("k8fy/system",          SYSTEM_PROMPT),
    ("k8fy/health-check",    HEALTH_SKILL_PROMPT),
    ("k8fy/cert-audit",      CERT_AUDIT_PROMPT),
    ("k8fy/change-history",  CHANGE_HISTORY_PROMPT),
    ("k8fy/restart-trend",   RESTART_TREND_PROMPT),
    ("k8fy/diagnose",        DIAGNOSE_PROMPT),
]


def main() -> None:
    public_key  = os.environ.get("LANGFUSE_PUBLIC_KEY", "")
    secret_key  = os.environ.get("LANGFUSE_SECRET_KEY", "")
    base_url    = os.environ.get("LANGFUSE_BASE_URL", "https://cloud.langfuse.com")

    if not public_key or not secret_key:
        print(
            "ERROR: LANGFUSE_PUBLIC_KEY and LANGFUSE_SECRET_KEY must be set.\n"
            "       Find them in Langfuse UI → Settings → API Keys.",
            file=sys.stderr,
        )
        sys.exit(1)

    lf = Langfuse(public_key=public_key, secret_key=secret_key, host=base_url)
    print(f"Connected to Langfuse at {base_url}\n")

    for name, content in PROMPTS:
        try:
            prompt = lf.create_prompt(
                name=name,
                type="text",
                prompt=content,
                labels=["production"],
            )
            print(f"  OK   {name}  (version {prompt.version})")
        except Exception as exc:
            print(f"  ERR  {name}  FAILED: {exc}", file=sys.stderr)

    print("\nDone. Verify in Langfuse UI > Prompts.")


if __name__ == "__main__":
    main()
