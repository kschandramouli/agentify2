from pydantic import AliasChoices, Field
from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    """Application settings loaded from environment variables."""

    # Server
    host: str = "0.0.0.0"
    port: int = 8001
    env: str = "dev"  # "dev" | "prod"

    # Claude API
    # Accept either ANTHROPIC_API_KEY (the canonical SDK name) or CLAUDE_API_KEY,
    # from the environment or src/agent/.env.
    claude_api_key: str = Field(
        default="",
        validation_alias=AliasChoices("ANTHROPIC_API_KEY", "CLAUDE_API_KEY"),
    )
    claude_model: str = "claude-opus-4-8"  # most capable current model
    claude_max_tokens: int = 4096  # room for adaptive thinking + the structured answer
    claude_effort: str = "high"  # low | medium | high | max — thinking/spend tradeoff
    agent_max_tool_iterations: int = 5  # cap on the tool-calling loop

    # Backend service (for the agent's tool callbacks)
    backend_url: str = "http://localhost:8080"

    # AWS
    aws_region: str = "ap-southeast-2"
    cloudwatch_log_group: str = "/agentify/agent"

    # Redis (for caching, future use)
    redis_url: str = "redis://localhost:6379"

    # Database (if agent needs to persist state)
    db_url: str = "postgresql://localhost/agentify"

    class Config:
        env_file = ".env"
        env_file_encoding = "utf-8"
        case_sensitive = False


def get_settings() -> Settings:
    """Get application settings."""
    return Settings()
