"""Create fresh local demo credentials without printing them."""
import argparse
import json
import zipfile
import base64
import os
from pathlib import Path
import secrets

path = Path(__file__).with_name(".env")
parser = argparse.ArgumentParser()
parser.add_argument("--public-url", help="HTTPS origin assigned to the demo tunnel")
args = parser.parse_args()
if path.exists():
    values = dict(line.split("=", 1) for line in path.read_text().splitlines() if line and not line.startswith("#"))
else:
    values = {name: secrets.token_hex(24) for name in ["DEMO_DATABASE_PASSWORD", "MCP_EVENTS_KEY", "SAMPLE_WEBHOOK_SECRET", "REPLAY_CAPTURE_TOKEN", "REPLAY_CONTROL_TOKEN", "REPLAY_OPERATOR_TOKEN"]}
    values["EVENT_ENCRYPTION_KEY"] = base64.b64encode(secrets.token_bytes(32)).decode()
public_url = args.public_url or values.get("DEMO_PUBLIC_URL", "http://localhost:18085")
if args.public_url and (not args.public_url.startswith("https://") or "/" in args.public_url[8:]):
    raise SystemExit("--public-url must be an HTTPS origin without a trailing slash")
values["DEMO_PUBLIC_URL"] = public_url
values["DEMO_ISSUER"] = public_url + "/demo"
values["DEMO_MCP_URL"] = public_url + "/agents/mcp"
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
with os.fdopen(fd, "w") as out:
    out.writelines(f"{key}={value}\n" for key, value in values.items())
runtime = path.with_name("runtime")
runtime.mkdir(exist_ok=True)
config = json.loads(path.with_name("idp.json").read_text())
for callback in config["tokenCallbacks"]:
    for mapping in callback["requestMappings"]:
        mapping["claims"]["iss"] = values["DEMO_ISSUER"]
(runtime / "idp.json").write_text(json.dumps(config, indent=2) + "\n")
plugin = {"$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json", "name": "hookway-event-demo", "version": "0.1.0", "description": "Private synthetic issue events and lookup tools for Hookway acceptance testing."}
mcp = {"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": {"hookway-event-demo": {"type": "streamable-http", "url": values["DEMO_MCP_URL"], "extensions": {"com.openai": {"auth": {"type": "oauth", "client": {"mode": "provided", "clientId": "demo-client", "tokenEndpointAuthMethod": "none"}, "authorizationUrl": values["DEMO_ISSUER"] + "/authorize", "tokenUrl": values["DEMO_ISSUER"] + "/token", "baseScopes": ["openid", "issues:read", "events:read"]}}}}}}
with zipfile.ZipFile(path.with_name("plugin.zip"), "w") as archive:
    archive.writestr("plugin.json", json.dumps(plugin, indent=2))
    archive.writestr("mcp.json", json.dumps(mcp, indent=2))
print("Prepared private demo .env, identity-provider config, and plugin.zip; credentials were not printed.")
