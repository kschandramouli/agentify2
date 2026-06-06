"""Tool definitions for the K8fy agent (Claude can call these)."""

import logging
from typing import Any, Dict

import httpx

logger = logging.getLogger(__name__)

# Tool schema for Claude to understand what it can call
TOOLS = [
    {
        "name": "query_pod",
        "description": "Query details about a specific pod: phase, ready status, restart count, recent events.",
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
            },
            "required": ["pod_id", "namespace"],
        },
    },
    {
        "name": "get_service_health",
        "description": "Get health status of a service: endpoints, ready ratio, pod statuses.",
        "input_schema": {
            "type": "object",
            "properties": {
                "service_name": {"type": "string", "description": "Service name"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
            },
            "required": ["service_name", "namespace"],
        },
    },
    {
        "name": "get_certificates",
        "description": "Get certificate status: list of certs, expiry dates, renewal needs.",
        "input_schema": {
            "type": "object",
            "properties": {
                "namespace": {
                    "type": "string",
                    "description": "Kubernetes namespace (optional, all if not provided)",
                },
            },
        },
    },
    {
        "name": "get_pod_events",
        "description": "Get recent events for a pod: restarts, crashes, warnings.",
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "limit": {"type": "integer", "description": "Number of recent events to return"},
            },
            "required": ["pod_id", "namespace"],
        },
    },
    {
        "name": "get_change_history",
        "description": (
            "Get recent deployment/change events (rollouts) for a service over a "
            "time window. Use this during diagnosis to see WHAT CHANGED before a "
            "symptom began — a rollout shortly before restarts started is a likely "
            "trigger to investigate (correlation, not proof)."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "deployment": {"type": "string", "description": "Deployment/service name to filter to."},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "since": {"type": "string", "description": "RFC3339 start of the window (optional)."},
                "until": {"type": "string", "description": "RFC3339 end of the window (optional)."},
            },
            "required": ["namespace"],
        },
    },
    {
        "name": "get_pod_logs",
        "description": (
            "Fetch a bounded, redacted tail of a pod's logs to find the CRASH REASON "
            "(OOMKilled, panic/stack trace, connection refused, failing probe). Set "
            "previous=true to read the last crashed container instance — that is where "
            "a CrashLoopBackOff reason usually is. Logs are best-effort redacted and "
            "not stored; quote the relevant failure line in your answer."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier"},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "container": {"type": "string", "description": "Container name (optional; defaults to the pod's container)."},
                "previous": {"type": "boolean", "description": "Read the previous (crashed) container instance."},
                "tail_lines": {"type": "integer", "description": "Lines from the end (default 100, capped server-side)."},
            },
            "required": ["pod_id", "namespace"],
        },
    },
    {
        "name": "get_metrics_history",
        "description": (
            "Get the restart-count time-series for a pod over a time window — to "
            "see WHEN restarts started climbing (the temporal trend), not just the "
            "current count. Use this when diagnosing to find when a problem began. "
            "Samples are cumulative restart counts; a rising series means restarts "
            "happened in that window."
        ),
        "input_schema": {
            "type": "object",
            "properties": {
                "pod_id": {"type": "string", "description": "Pod identifier to filter the series to."},
                "namespace": {"type": "string", "description": "Kubernetes namespace"},
                "since": {"type": "string", "description": "RFC3339 start of the window (optional)."},
                "until": {"type": "string", "description": "RFC3339 end of the window (optional)."},
                "order": {"type": "string", "enum": ["asc", "desc"], "description": "Chronological (asc) or recent-first (desc). Use asc to read a trend."},
                "limit": {"type": "integer", "description": "Max samples (default 100)."},
            },
            "required": ["pod_id", "namespace"],
        },
    },
]


async def process_tool_call(
    tool_name: str, arguments: Dict[str, Any], backend_url: str, timeout: float = 10.0
) -> Dict[str, Any]:
    """Execute a tool call by fetching live data from the backend.

    Each tool maps to a pod query on the backend (see backend HandleAgentFetch).
    Returns the fetched data, or an error dict the model can reason about — a
    failed tool call must not crash the agent's loop.
    """
    known = {t["name"] for t in TOOLS}
    if tool_name not in known:
        return {"error": f"Unknown tool: {tool_name}"}

    url = f"{backend_url.rstrip('/')}/api/agent/fetch"
    payload = {"tool": tool_name, "args": arguments}

    try:
        async with httpx.AsyncClient(timeout=timeout) as client:
            resp = await client.post(url, json=payload)
            resp.raise_for_status()
            body = resp.json()
    except httpx.HTTPError as exc:
        logger.error("tool fetch failed", extra={"tool": tool_name, "error": str(exc)})
        return {"error": f"backend fetch failed for {tool_name}: {exc}"}

    # body is {"tool": ..., "data": {pod_id: [rows], ...}}
    return body.get("data", {})
