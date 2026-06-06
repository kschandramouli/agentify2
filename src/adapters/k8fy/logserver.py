"""On-demand pod-log server for the K8fy adapter (spec 008 / ADR 0014).

The adapter holds the Kubernetes client, so it is the only component that can read
pod logs. This exposes a single internal endpoint, ``POST /logs``, that the backend
calls during diagnosis to fetch a bounded tail of a pod's (optionally previous,
crashed) container.

Logs are returned raw over this internal hop and are **never persisted** by the
adapter. The backend applies best-effort text scrubbing at the egress boundary
before the agent/model sees them (ADR 0007 / ADR 0014).

A stdlib HTTP server is used deliberately: a single internal route does not justify
pulling an ASGI framework into the adapter image.
"""

import hmac
import json
import logging
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Optional

from kubernetes import client
from kubernetes.client.rest import ApiException

logger = logging.getLogger("agentify.k8fy.logserver")


def check_auth(auth_header: Optional[str], expected_token: str) -> bool:
    """Validate a Bearer token in constant time.

    When no token is configured the endpoint is open (dev convenience); prod must
    set ADAPTER_AUTH_TOKEN. Comparison uses hmac.compare_digest to avoid leaking
    the token via timing.
    """
    if not expected_token:
        return True
    if not auth_header:
        return False
    prefix = "Bearer "
    if not auth_header.startswith(prefix):
        return False
    return hmac.compare_digest(auth_header[len(prefix):], expected_token)


class LogReader:
    """Reads a bounded tail of a pod's logs via the Kubernetes API."""

    def __init__(self, core_v1: client.CoreV1Api, default_namespace: str, max_tail_lines: int = 200):
        self._core = core_v1
        self._default_namespace = default_namespace
        self._max_tail_lines = max_tail_lines

    def read(
        self,
        pod_id: str,
        namespace: str = "",
        container: str = "",
        previous: bool = False,
        tail_lines: int = 100,
    ) -> dict[str, Any]:
        """Return a bounded log tail, or an error payload the agent can reason about.

        tail_lines is clamped to [1, max_tail_lines] regardless of the request, so a
        caller can never pull an unbounded volume.
        """
        ns = namespace or self._default_namespace
        tail = max(1, min(int(tail_lines or 100), self._max_tail_lines))
        try:
            text = self._core.read_namespaced_pod_log(
                name=pod_id,
                namespace=ns,
                container=container or None,
                previous=bool(previous),
                tail_lines=tail,
                timestamps=True,
            )
        except ApiException as exc:
            logger.warning(
                "log read failed",
                extra={"pod_id": pod_id, "namespace": ns, "status": exc.status},
            )
            return {
                "pod_id": pod_id,
                "namespace": ns,
                "container": container,
                "previous": bool(previous),
                "logs": "",
                "error": f"could not read logs (status {exc.status})",
            }
        return {
            "pod_id": pod_id,
            "namespace": ns,
            "container": container,
            "previous": bool(previous),
            "logs": text or "",
        }


def _make_handler(reader: LogReader, auth_token: str = ""):
    class _Handler(BaseHTTPRequestHandler):
        # Quiet the default per-request stderr logging; we log failures ourselves.
        def log_message(self, *args: Any) -> None:  # noqa: D401
            return

        def _send(self, status: int, body: dict[str, Any]) -> None:
            payload = json.dumps(body).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def do_GET(self) -> None:  # noqa: N802 (stdlib naming)
            if self.path == "/health":
                self._send(200, {"status": "ok"})
            else:
                self._send(404, {"error": "not found"})

        def do_POST(self) -> None:  # noqa: N802
            if self.path != "/logs":
                self._send(404, {"error": "not found"})
                return
            if not check_auth(self.headers.get("Authorization"), auth_token):
                self._send(401, {"error": "unauthorized"})
                return
            try:
                length = int(self.headers.get("Content-Length", 0))
                req = json.loads(self.rfile.read(length) or b"{}")
            except (ValueError, json.JSONDecodeError):
                self._send(400, {"error": "bad request"})
                return
            pod_id = req.get("pod_id")
            if not pod_id:
                self._send(400, {"error": "pod_id required"})
                return
            result = reader.read(
                pod_id=pod_id,
                namespace=req.get("namespace", ""),
                container=req.get("container", ""),
                previous=req.get("previous", False),
                tail_lines=req.get("tail_lines", 100),
            )
            self._send(200, result)

    return _Handler


def serve_logs(reader: LogReader, port: int, auth_token: str = "") -> None:
    """Run the log server (blocking). Intended to run on a daemon thread."""
    if not auth_token:
        logger.warning("log server auth token not set; /logs is unauthenticated (set ADAPTER_AUTH_TOKEN in prod)")
    server = ThreadingHTTPServer(("0.0.0.0", port), _make_handler(reader, auth_token))
    logger.info("log server listening", extra={"port": port})
    server.serve_forever()
