"""FastAPI application setup for the agent service."""

from fastapi import FastAPI, HTTPException, Response
import logging

import metrics
from config.settings import get_settings
from k8fy.agent import get_chat_agent, refresh_pricing_from_backend
from k8fy.skills.router import get_skill_router
from models.response import AgentResponse, QueryRequest
from typing import Any, Dict, List, Optional

# Setup logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

settings = get_settings()

# Create FastAPI app
app = FastAPI(title="agentify-agent", version="0.1.0")


class ChatRequest(BaseModel):
    """Request body for the multi-turn chat endpoint."""
    messages: List[Dict[str, Any]]  # [{role: "user"|"assistant", content: "..."}]
    context: Dict[str, Any] = {}    # {namespace, service, ...}
    trace_id: Optional[str] = None


@app.on_event("startup")
async def startup_event():
    """Initialize skill router (and all sub-agents) on startup."""
    logger.info("Agent service starting up...")
    refresh_pricing_from_backend(settings.backend_url)
    get_skill_router()
    get_chat_agent()  # warm the chat agent singleton
    logger.info(f"Skill router initialized with model: {settings.claude_model}")


@app.get("/health")
async def health_check():
    """Health check endpoint."""
    return {"status": "ok", "service": "agentify-agent"}


@app.get("/metrics")
async def metrics_endpoint():
    """Prometheus metrics: token usage + indicative cost (ADR 0011)."""
    body, content_type = metrics.exposition()
    return Response(content=body, media_type=content_type)


@app.post("/reason", response_model=AgentResponse)
async def reason(request: QueryRequest) -> AgentResponse:
    """Reason about a query and return an answer.

    Request body:
    {
      "intent": "health_check",
      "data": { ... },
      "context": { "namespace": "prod" }
    }

    Response:
    {
      "answer": "...",
      "status": "healthy",
      "confidence": 0.95,
      "sources": ["k8fy.live-state"],
      "details": { ... }
    }
    """
    # Log the propagated trace_id so the agent's reasoning correlates with the
    # backend's query.trace by the same id (spec 004).
    logger.info("reason request", extra={"trace_id": request.trace_id, "intent": request.intent})
    try:
        response = await get_skill_router().dispatch(request.intent, request.data, request.context)
        return response
    except Exception as e:
        logger.error(f"Reasoning error (trace_id=%s): %s", request.trace_id, e)
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/reason-chat", response_model=AgentResponse)
async def reason_chat(request: ChatRequest) -> AgentResponse:
    """Multi-turn conversational reasoning endpoint.

    Accepts the full conversation history and responds using the chat agent
    (free-form prose, agentic tool use, no JSON schema constraint).
    """
    logger.info(
        "chat request",
        extra={"trace_id": request.trace_id, "turns": len(request.messages)},
    )
    try:
        response = await get_chat_agent().reason_chat(request.messages, request.context)
        return response
    except Exception as e:
        logger.error("Chat reasoning error (trace_id=%s): %s", request.trace_id, e)
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/")
async def root():
    """API root."""
    return {
        "service": "agentify-agent",
        "version": "0.1.0",
        "endpoints": [
            "/health",
            "/reason",
        ],
    }
