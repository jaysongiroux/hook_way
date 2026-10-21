#!/usr/bin/env python3
"""Signed MCP callback receiver using an operator-supplied HTTPS certificate.

MCP_CALLBACK_SECRET=whsec_<base64-key> python3 callback_receiver.py \
  --cert certificate.pem --key private-key.pem --port 8443
Use that same secret when subscribing. No Hookway caller credentials are needed.
"""
import argparse
import base64
import hashlib
import hmac
import json
import os
import ssl
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Receiver(BaseHTTPRequestHandler):
    secret = b""

    def do_POST(self):
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 262144:
                raise ValueError("length")
            body = self.rfile.read(length)
            identity = self.headers["webhook-id"]
            stamp = self.headers["webhook-timestamp"]
            if abs(time.time() - int(stamp)) > 300:
                raise ValueError("timestamp")
            digest = hmac.new(self.secret, f"{identity}.{stamp}.".encode() + body, hashlib.sha256).digest()
            expected = "v1," + base64.b64encode(digest).decode()
            if not any(hmac.compare_digest(expected, value) for value in self.headers.get("webhook-signature", "").split()):
                raise ValueError("signature")
            event = json.loads(body)
        except (ValueError, TypeError, KeyError):
            self.send_error(400, "Invalid signed callback")
            return
        if event.get("type") == "verification":
            result = {"challenge": event["challenge"]}
        else:
            print(json.dumps(event), flush=True)
            result = {}
        raw = json.dumps(result).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, *_args):
        pass  # Do not log callback URLs or signatures.


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cert", required=True)
    parser.add_argument("--key", required=True)
    parser.add_argument("--port", type=int, default=8443)
    args = parser.parse_args()
    value = os.environ["MCP_CALLBACK_SECRET"]
    if not value.startswith("whsec_"):
        raise ValueError("MCP_CALLBACK_SECRET must start with whsec_")
    Receiver.secret = base64.b64decode(value[6:], validate=True)
    if not 24 <= len(Receiver.secret) <= 64:
        raise ValueError("Signing key must contain 24 to 64 bytes")
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(args.cert, args.key)
    server = ThreadingHTTPServer(("0.0.0.0", args.port), Receiver)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
