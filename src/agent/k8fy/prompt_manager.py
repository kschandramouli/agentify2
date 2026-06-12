"""Langfuse-backed prompt management with local fallback.

Prompts are stored and versioned in Langfuse under the label "production".
When LANGFUSE_PUBLIC_KEY is set the agent fetches live prompts on startup;
the Langfuse SDK caches them internally (default TTL: 60 s), so updates made
in the Langfuse UI are picked up without a service restart.

If credentials are absent, or if a prompt name does not yet exist in Langfuse,
the local fallback string from prompts.py is used silently.  The service
therefore starts cleanly with or without Langfuse configured.

Prompt names used by this codebase:
  k8fy/system          — general-purpose fallback (K8fyAgent)
  k8fy/health-check    — HealthSkill
  k8fy/cert-audit      — CertAuditSkill
  k8fy/change-history  — ChangeHistorySkill
  k8fy/restart-trend   — RestartTrendSkill
  k8fy/diagnose        — DiagnoseSkill
"""

import logging

logger = logging.getLogger(__name__)

_langfuse = None
_initialised = False


def _get_client():
    """Return a Langfuse client, or None if credentials are not configured."""
    global _langfuse, _initialised
    if _initialised:
        return _langfuse

    _initialised = True
    from config.settings import settings  # imported lazily to avoid circular deps

    if not settings.langfuse_public_key:
        logger.info(
            "LANGFUSE_PUBLIC_KEY not set — prompt management disabled; using local prompts"
        )
        return None

    try:
        from langfuse import Langfuse

        _langfuse = Langfuse(
            public_key=settings.langfuse_public_key,
            secret_key=settings.langfuse_secret_key,
            base_url=settings.langfuse_base_url,
        )
        logger.info(
            "Langfuse prompt management enabled",
            extra={"host": settings.langfuse_base_url},
        )
    except Exception as exc:
        logger.warning("Langfuse init failed — using local prompts: %s", exc)

    return _langfuse


def get_prompt(name: str, fallback: str) -> str:
    """Return the prompt text for *name* from Langfuse (production label).

    Falls back to *fallback* if Langfuse is not configured, the prompt does
    not exist, or any network/API error occurs.
    """
    client = _get_client()
    if client is None:
        return fallback
    try:
        return client.get_prompt(name, label="production").compile()
    except Exception as exc:
        logger.warning(
            "Langfuse get_prompt('%s') failed — using local fallback: %s", name, exc
        )
        return fallback
