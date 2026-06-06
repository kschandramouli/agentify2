"""FastAPI application setup for the agent service."""

from fastapi import FastAPI, HTTPException, Response
import logging

import metrics
from config.settings import get_settings
from k8fy.agent import get_k8fy_agent
from models.response import AgentResponse, QueryRequest

# Setup logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

settings = get_settings()

# Create FastAPI app
app = FastAPI(title="agentify-agent", version="0.1.0")


@app.on_event("startup")
async def startup_event():
    """Initialize agent on startup."""
    logger.info("Agent service starting up...")
    agent = get_k8fy_agent()
    logger.info(f"Agent initialized with model: {settings.claude_model}")


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
        agent = get_k8fy_agent()
        response = await agent.reason(request.intent, request.data, request.context)
        return response
    except Exception as e:
        logger.error(f"Reasoning error (trace_id=%s): %s", request.trace_id, e)
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
