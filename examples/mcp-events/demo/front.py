"""Allowlisted demo tunnel front. Operator, control, metrics and webhook routes stay private."""
import http.client
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit
PUBLIC = os.environ.get("DEMO_PUBLIC_URL", "http://localhost:18085")
class Handler(BaseHTTPRequestHandler):
    def proxy(self):
        path = urlsplit(self.path).path
        if path in {"/agents/mcp", "/.well-known/oauth-protected-resource/agents/mcp"}:
            upstream = "core"
        elif path in {"/demo/authorize", "/demo/token", "/demo/jwks", "/demo/userinfo", "/demo/revoke", "/demo/.well-known/openid-configuration", "/demo/.well-known/oauth-authorization-server"}:
            upstream = "idp"
        else:
            self.send_error(404); return
        length = int(self.headers.get("Content-Length", "0"))
        if length > 1048576:
            self.send_error(413); return
        body = self.rfile.read(length) if length else None
        headers = {key: value for key, value in self.headers.items() if key.lower() not in ("host", "connection", "transfer-encoding", "accept-encoding")}
        headers["Host"] = urlsplit(PUBLIC).netloc
        connection = http.client.HTTPConnection(upstream, 8080, timeout=30)
        try:
            connection.request(self.command, self.path, body, headers)
            response = connection.getresponse()
            content = response.read(2 * 1024 * 1024)
            if upstream == "idp" and ".well-known" in path:
                content = content.replace(("http://" + urlsplit(PUBLIC).netloc).encode(), PUBLIC.encode())
            self.send_response(response.status)
            for key, value in response.getheaders():
                if key.lower() not in ("content-length", "connection", "transfer-encoding"):
                    self.send_header(key, value)
            self.send_header("Content-Length", str(len(content)))
            self.end_headers(); self.wfile.write(content)
        finally:
            connection.close()
    do_GET = do_POST = do_OPTIONS = proxy
    def log_message(self, *_):
        pass
if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
