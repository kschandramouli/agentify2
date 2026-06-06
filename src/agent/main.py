"""Entry point for the K8fy agent service."""

import uvicorn
import logging
from config.settings import get_settings

logger = logging.getLogger(__name__)

if __name__ == "__main__":
    settings = get_settings()

    logger.info(f"Starting agent service on {settings.host}:{settings.port}")
    logger.info(f"Environment: {settings.env}")
    logger.info(f"Claude model: {settings.claude_model}")

    uvicorn.run(
        "app:app",
        host=settings.host,
        port=settings.port,
        reload=(settings.env == "dev"),
        log_level="info",
    )
