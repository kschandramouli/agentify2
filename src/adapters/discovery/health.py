"""health.py — a stdlib liveness/readiness endpoint for agentify-discovery.

A stdlib HTTP server is used deliberately, same justification as
src/adapters/k8fy/logserver.py: a single internal route does not justify
pulling an ASGI framework into this image.
"""

import json
import logging
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

logger = logging.getLogger(__name__)


class _Handler(BaseHTTPRequestHandler):
    def log_message(self, *args) -> None:  # noqa: D401 — quiet default stderr logging
        return

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/health":
            body = json.dumps({"status": "ok"}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()


def serve_health(port: int) -> None:
    """Run the health server (blocking). Intended to run on a daemon thread."""
    server = ThreadingHTTPServer(("0.0.0.0", port), _Handler)
    logger.info("health server listening on port %d", port)
    server.serve_forever()
