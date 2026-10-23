"""Automated OAuth + MCP tool check. This is protocol proof, not hosted-agent proof."""
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import urllib.parse
import urllib.request

BASE = os.environ.get("DEMO_BASE_URL", "http://localhost:18080")
IDP = os.environ.get("DEMO_IDP_URL", "http://localhost:18086/demo")
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_):
        return None

def token(client="demo-client"):
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
    redirect = "http://localhost:18087/callback"
    query = urllib.parse.urlencode(dict(response_type="code", client_id=client, redirect_uri=redirect, scope="openid issues:read events:read", state="demo-check", code_challenge=challenge, code_challenge_method="S256"))
    try:
        urllib.request.build_opener(NoRedirect).open(f"{IDP}/authorize?{query}", timeout=15)
        raise AssertionError("Authorization did not redirect")
    except urllib.error.HTTPError as response:
        assert response.code == 302
        location = urllib.parse.urlparse(response.headers["Location"])
        params = urllib.parse.parse_qs(location.query)
        assert params["state"] == ["demo-check"]
        code = params["code"][0]
    body = urllib.parse.urlencode(dict(grant_type="authorization_code", client_id=client, redirect_uri=redirect, code=code, code_verifier=verifier)).encode()
    with urllib.request.urlopen(f"{IDP}/token", data=body, timeout=15) as response:
        return json.load(response)["access_token"]

def rpc(credential, method, params, version="2026-07-28"):
    if version == "2026-07-28":
        params["_meta"] = {"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientCapabilities": {}, "io.modelcontextprotocol/clientInfo": {"name": "hookway-demo-check", "version": "1"}}
    body = json.dumps(dict(jsonrpc="2.0", id=1, method=method, params=params)).encode()
    req = urllib.request.Request(BASE + "/agents/mcp", data=body, headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream", "Authorization": "Bearer " + credential, "MCP-Protocol-Version": version, "MCP-Method": method})
    with urllib.request.urlopen(req, timeout=20) as response:
        raw = response.read().decode()
    if raw.startswith("event:"):
        raw = next(line[6:] for line in raw.splitlines() if line.startswith("data: "))
    value = json.loads(raw)
    assert "error" not in value, value
    return value["result"]

if __name__ == "__main__":
    credential = token()
    claims = json.loads(base64.urlsafe_b64decode(credential.split(".")[1] + "=="))
    assert claims["sub"] == "demo-agent"
    events = rpc(credential, "events/list", {})
    assert any(event["name"] == "issue.opened" for event in events["events"])
    tools = rpc(credential, "tools/list", {}, "2025-11-25")
    assert any(tool["name"] == "get_issue" for tool in tools["tools"])
    result = rpc(credential, "tools/call", {"name": "get_issue", "arguments": {"issue_id": "42"}}, "2025-11-25")
    assert "fixture-detail-42" in json.dumps(result), result
    result = rpc(credential, "tools/call", {"name": "get_issue", "arguments": {"issue_id": "42001"}}, "2025-11-25")
    assert "fixture-detail-42001" in json.dumps(result), result
    try:
        rpc(token("invalid-audience"), "events/list", {})
        raise AssertionError("Wrong audience accepted")
    except urllib.error.HTTPError as response:
        assert response.code in (401, 403)
    print("PASS: OAuth authorization code + S256 PKCE, event discovery, get_issue, and wrong-audience rejection. Hosted-agent wake-up not established.")
