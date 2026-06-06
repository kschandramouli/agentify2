"""Emit canonical events to the backend ingestion endpoint."""

import logging
from typing import Any

import requests

logger = logging.getLogger("agentify.k8fy.emitter")


class Emitter:
    """Posts canonical events to the backend's POST /api/ingest endpoint."""

    def __init__(self, ingest_url: str, auth_token: str = "", timeout: float = 5.0):
        self._url = ingest_url
        self._timeout = timeout
        self._session = requests.Session()
        if auth_token:
            self._session.headers["Authorization"] = f"Bearer {auth_token}"
        self._session.headers["Content-Type"] = "application/json"

    def emit(self, event: dict[str, Any]) -> bool:
        """Send one event. Returns True on success; logs and returns False on failure.

        Emission is best-effort: a single dropped event must not crash the watch
        loop, so transport errors are caught and logged rather than raised.
        """
        try:
            resp = self._session.post(self._url, json=event, timeout=self._timeout)
        except requests.RequestException as exc:
            logger.error(
                "failed to emit event",
                extra={"event_type": event.get("type"), "error": str(exc)},
            )
            return False

        if resp.status_code >= 300:
            logger.error(
                "backend rejected event",
                extra={
                    "event_type": event.get("type"),
                    "status": resp.status_code,
                    "body": resp.text[:500],
                },
            )
            return False

        logger.debug(
            "event emitted",
            extra={"event_type": event.get("type"), "status": resp.status_code},
        )
        return True
