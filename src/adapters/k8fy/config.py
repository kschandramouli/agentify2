"""Configuration for the K8fy adapter, loaded from environment variables."""

import os
from dataclasses import dataclass


@dataclass
class Config:
    """Runtime configuration for the K8fy adapter."""

    backend_url: str
    backend_auth_token: str
    namespace: str
    scrape_interval: int  # seconds, for metrics polling
    cert_check_interval: int  # seconds, for certificate scraping
    log_server_port: int  # on-demand log endpoint the backend calls (spec 008)
    max_tail_lines: int  # hard cap on log tail length
    adapter_auth_token: str  # bearer token guarding /logs (empty = open, dev only)

    @property
    def ingest_url(self) -> str:
        """Full URL of the backend ingestion endpoint."""
        return f"{self.backend_url.rstrip('/')}/api/ingest"


def load_from_env() -> Config:
    """Load configuration from environment variables, applying sensible defaults."""
    return Config(
        backend_url=os.getenv("BACKEND_URL", "http://localhost:8080"),
        backend_auth_token=os.getenv("BACKEND_AUTH_TOKEN", ""),
        namespace=os.getenv("K8S_NAMESPACE", "default"),
        scrape_interval=_int_env("SCRAPE_INTERVAL", 30),
        cert_check_interval=_int_env("CERT_CHECK_INTERVAL", 300),
        log_server_port=_int_env("LOG_SERVER_PORT", 8200),
        max_tail_lines=_int_env("MAX_TAIL_LINES", 200),
        adapter_auth_token=os.getenv("ADAPTER_AUTH_TOKEN", ""),
    )


def _int_env(key: str, default: int) -> int:
    value = os.getenv(key)
    if not value:
        return default
    try:
        return int(value)
    except ValueError:
        return default
