"""Send a deterministic matching or nonmatching issue webhook."""
import argparse
import json
import os
from pathlib import Path
import urllib.request
parser = argparse.ArgumentParser()
parser.add_argument("--nonmatching", action="store_true")
parser.add_argument("--event-id", required=True, help="Use a fresh ID for a new occurrence; reuse it to test duplicate acceptance.")
args = parser.parse_args()
if not args.event_id.isascii() or not args.event_id.isdecimal() or len(args.event_id) > 10:
    parser.error("--event-id must contain 1–10 digits for the issue lookup tool")
values = dict(line.split("=", 1) for line in Path(__file__).with_name(".env").read_text().splitlines() if line and not line.startswith("#"))
fixture = json.loads(Path(__file__).with_name("nonmatching.json" if args.nonmatching else "matching.json").read_text())
body = json.dumps(dict(fixture, issue_id=args.event_id)).encode()
request = urllib.request.Request(os.environ.get("DEMO_BASE_URL", "http://localhost:18080") + "/webhooks/issues", data=body, headers={"Content-Type": "application/json", "X-Api-Key": values["SAMPLE_WEBHOOK_SECRET"]})
with urllib.request.urlopen(request, timeout=20) as response:
    print("Webhook acknowledged:", response.status)
