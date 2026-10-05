#!/usr/bin/env python3
"""Docker startup: write the public API URL and Nginx configuration.

The React bundle is static. This helper lets the same image use a different
replay API URL without rebuilding it, and limits its CSP to that API origin.
"""
import ipaddress
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit, urlunsplit


def api_url(value: str) -> tuple[str, str]:
    """Return a normalized base URL and its CSP-safe origin."""
    if not value or len(value) > 2048 or re.search(r"[\s\\\x00-\x1f\x7f]", value):
        raise ValueError("REPLAY_API_URL must be a nonempty absolute HTTP(S) URL")
    try:
        parsed = urlsplit(value)
        host = (parsed.hostname or "").lower()
        port = parsed.port
    except ValueError:
        raise ValueError("REPLAY_API_URL contains an invalid hostname or port") from None
    if parsed.scheme not in ("http", "https") or not host:
        raise ValueError("REPLAY_API_URL must be an absolute HTTP(S) URL")
    if parsed.username is not None or parsed.password is not None or "?" in value or "#" in value:
        raise ValueError("REPLAY_API_URL must not contain credentials, a query or fragment")
    if ":" in host:
        try:
            if "%" in host:
                raise ValueError
            ipaddress.IPv6Address(host)
        except ValueError:
            raise ValueError("REPLAY_API_URL contains an invalid hostname") from None
        host = "[" + host + "]"
    elif len(host) > 253 or any(not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", label) for label in host.split(".")):
        raise ValueError("REPLAY_API_URL contains an invalid hostname")
    if re.fullmatch(r"[0-9.]+", host):
        try:
            ipaddress.IPv4Address(host)
        except ValueError:
            raise ValueError("REPLAY_API_URL contains an invalid hostname") from None
    if port is not None and not 1 <= port <= 65535:
        raise ValueError("REPLAY_API_URL contains an invalid port")
    authority = host + (":" + str(port) if port and port != {"http": 80, "https": 443}[parsed.scheme] else "")
    path = parsed.path.rstrip("/")
    if not re.fullmatch(r"[A-Za-z0-9._~!$&'()*+,;=:@/%-]*", path) or re.search(r"%(?![0-9a-fA-F]{2})", path):
        raise ValueError("REPLAY_API_URL contains an invalid path")
    if any(part.lower().replace("%2e", ".") in (".", "..") for part in path.split("/")):
        raise ValueError("REPLAY_API_URL must not contain dot path segments")
    origin = urlunsplit((parsed.scheme, authority, "", "", ""))
    return origin + path, origin


def configure(value: str, directory: Path, web_root: Path) -> None:
    base, origin = api_url(value)
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "runtime-config.json").write_text(json.dumps({"replayApiUrl": base}) + "\n", encoding="utf-8")
    # Only the validated origin enters an HTTP header. Environment text never
    # becomes executable JS, an Nginx directive, or a proxy destination.
    csp = ("default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; "
           "style-src 'self' 'unsafe-inline'; img-src 'self' data:; "
           "font-src 'self'; connect-src 'self' " + origin + "; "
           "object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'none'")
    config = """pid /tmp/hookway-ui/nginx.pid;
error_log /dev/stderr warn;
worker_processes auto;
events { worker_connections 1024; }
http {
    include /etc/nginx/mime.types;
    default_type application/octet-stream;
    access_log /dev/stdout;
    server_tokens off;
    client_body_temp_path /tmp/hookway-ui/client-body;
    proxy_temp_path /tmp/hookway-ui/proxy;
    fastcgi_temp_path /tmp/hookway-ui/fastcgi;
    uwsgi_temp_path /tmp/hookway-ui/uwsgi;
    scgi_temp_path /tmp/hookway-ui/scgi;
    client_max_body_size 1k;
    client_header_timeout 10s;
    client_body_timeout 10s;
    send_timeout 30s;
    server {
        listen 8080;
        root WEB_ROOT;
        index console.html;
        if ($request_method !~ ^(GET|HEAD)$) { return 405; }
        add_header Cache-Control "no-store" always;
        add_header X-Content-Type-Options "nosniff" always;
        add_header Referrer-Policy "no-referrer" always;
        add_header X-Frame-Options "DENY" always;
        add_header Permissions-Policy "camera=(), microphone=(), geolocation=()" always;
        add_header Content-Security-Policy "CSP_VALUE" always;
        location = /healthz { default_type text/plain; return 200 "ok\\n"; }
        location = /runtime-config.json { alias RUNTIME_FILE; default_type application/json; }
        location = / { try_files /console.html =404; }
        location = /builder { try_files /console.html =404; }
        location = /builder/ { return 308 /builder; }
        location ~ (^|/)\\. { return 404; }
        location / { try_files $uri =404; }
    }
}
"""
    # Paths below are application-owned constants, never runtime input.
    config = config.replace("WEB_ROOT", str(web_root)).replace("RUNTIME_FILE", str(directory / "runtime-config.json")).replace("CSP_VALUE", csp)
    (directory / "nginx.conf").write_text(config, encoding="utf-8")


if __name__ == "__main__":
    try:
        configure(os.environ.get("REPLAY_API_URL", ""), Path("/tmp/hookway-ui"), Path("/usr/share/nginx/html"))
    except ValueError as error:
        # Never echo the supplied URL: a rejected value could contain a secret.
        print("Frontend configuration rejected: " + str(error), file=sys.stderr)
        sys.exit(1)
