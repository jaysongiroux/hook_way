#!/usr/bin/env python3
"""Generate Go-owned UI contracts, or compare temporary outputs without writes."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(*args, **kwargs):
    return subprocess.run(args, cwd=ROOT, check=True, **kwargs)


def generate(kind, temp):
    outputs = {}
    go = os.environ.get("GO", "go")
    if kind == "nodes":
        for name, flag in [("nodeSchema", "--nodes"), ("destinationSchema", "--destinations"), ("connectionSchema", "--connections"), ("fullSchema", None)]:
            raw = run(go, "run", "./cmd/hookway", "schema", *([flag] if flag else []), stdout=subprocess.PIPE).stdout
            parsed = json.loads(raw)
            if flag:
                outputs[ROOT / f"web/src/graph/{name}.json"] = raw
            else:
                workspace = {key: parsed["properties"][key] for key in ["routing", "credentials", "lookups", "state_stores", "launchdarkly"]}
                outputs[ROOT / "web/src/graph/workspaceSchema.json"] = (json.dumps(workspace, indent=2) + "\n").encode()
    else:
        run(go, "run", "./cmd/hookway-openapi", "--out", str(temp))
        for name in ["editor", "replay", "authoring"]:
            source = temp / f"{name}.openapi.json"
            json.loads(source.read_bytes())
            outputs[ROOT / f"docs/api/{source.name}"] = source.read_bytes()
            dest = temp / f"{name}.ts"
            run(str(ROOT / "web/node_modules/.bin/openapi-typescript"), str(source), "--output", str(dest), "--default-non-nullable", "false")
            outputs[ROOT / f"web/src/shared/contracts/generated/{name}.ts"] = dest.read_bytes()
        outputs[ROOT / "docs/api/openapi-import.settings.schema.json"] = (temp / "openapi-import.settings.schema.json").read_bytes()
        outputs[ROOT / "web/src/graph/openapiSettingsSchema.json"] = (temp / "openapi-import.settings.schema.json").read_bytes()
    return outputs


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=["nodes", "api"])
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="hookway-contracts-") as folder:
        outputs = generate(args.kind, Path(folder))
        if args.check:
            drift = [str(path.relative_to(ROOT)) for path, data in outputs.items() if not path.exists() or path.read_bytes() != data]
            if drift:
                parser.exit(1, "Generated contract drift:\n" + "\n".join(drift) + f"\nRun make {'node' if args.kind == 'nodes' else 'api'}-schema.\n")
            print(f"{args.kind} contracts match; no files changed")
        else:
            for path, data in outputs.items():
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)


if __name__ == "__main__":
    main()
