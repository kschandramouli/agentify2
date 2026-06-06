from anthropic import AsyncAnthropic

from config.settings import get_settings

settings = get_settings()

# Async client — the FastAPI handlers and the agent loop are async, so use the
# async SDK to avoid blocking the event loop on each API call.
# Falls back to ANTHROPIC_API_KEY from the environment when claude_api_key is unset.
client = AsyncAnthropic(api_key=settings.claude_api_key or None)


def get_claude_client() -> AsyncAnthropic:
    """Get the async Anthropic client instance."""
    return client
