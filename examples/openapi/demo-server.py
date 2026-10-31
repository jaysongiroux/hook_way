"""Loopback-only synthetic customer API for the OpenAPI-to-MCP walkthrough."""
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        path = urlsplit(self.path).path
        if not path.startswith("/customers/") or not path.removeprefix("/customers/"):
            self.send_error(404)
            return
        body = json.dumps({"id": path.removeprefix("/customers/"), "name": "Sample customer"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    print("Synthetic customer API: http://127.0.0.1:18090", flush=True)
    ThreadingHTTPServer(("127.0.0.1", 18090), Handler).serve_forever()
