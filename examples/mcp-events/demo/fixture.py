"""Deterministic, read-only fixture API. No model or external service credentials."""
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

ISSUES = {
    "42": {"issue_id": "42", "repository": "acme/app", "title": "Investigate slow checkout", "detail": "The payment adapter exceeded its 800 ms budget.", "lookup_evidence": "fixture-detail-42"},
    "43": {"issue_id": "43", "repository": "other/app", "title": "Unrelated issue", "detail": "This issue must not match the acme/app subscription.", "lookup_evidence": "fixture-detail-43"},
}
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        path = urlsplit(self.path).path
        value = ISSUES.get("43" if path.removeprefix("/issues/")=="43" else "42") if path.startswith("/issues/") else None
        if value is not None:
            issue_id = path.removeprefix("/issues/")
            value = dict(value, issue_id=issue_id, lookup_evidence="fixture-detail-" + issue_id)
        if path == "/healthz":
            value = {"status": "ready"}
        body = json.dumps(value or {"error": "not_found"}).encode()
        self.send_response(200 if value else 404)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *_):
        pass
if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
