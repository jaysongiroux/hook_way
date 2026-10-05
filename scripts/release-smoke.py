#!/usr/bin/env python3
"""Pull and exercise immutable release images; optionally rehearse binary rollback.

This isolated smoke config has no destinations or replay store. Database migration,
restore, destination receipts and load-balancer behavior need deployment acceptance.
"""
import argparse
import json
import pathlib
import re
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT).strip()


def checked_image(image, version, commit, static=False):
    if not re.fullmatch(r"[^\s@]+@sha256:[a-f0-9]{64}", image):
        raise ValueError("use an immutable image@sha256 digest")
    run("docker", "pull", "--platform", "linux/amd64", image)
    info = json.loads(run("docker", "image", "inspect", image))[0]
    if (info["Os"], info["Architecture"]) != ("linux", "amd64"):
        raise RuntimeError("release architecture must be linux/amd64")
    if static:
        return {"image": image, "image_id": info["Id"], "platform": "linux/amd64", **console_install(image, version, commit)}
    output = run("docker", "run", "--rm", "--platform", "linux/amd64", image, "version")
    # CLI versions use whitespace-delimited version/commit values. Check whole
    # tokens so v1.2.3 cannot accidentally match v1.2.30.
    if version not in output.split() or f"({commit})" not in output.split():
        raise RuntimeError(f"unexpected build identity: {output}")
    return {"image": image, "image_id": info["Id"], "platform": "linux/amd64", "version_output": output}


def core_install(image, config):
    container = run("docker", "run", "-d", "--platform", "linux/amd64", "-p", "127.0.0.1::8080",
                    "-v", f"{config}:/config.yaml:ro", image, "serve", "--config", "/config.yaml")
    try:
        port = run("docker", "port", container, "8080/tcp").rsplit(":", 1)[1]
        base = f"http://127.0.0.1:{port}"
        for attempt in range(100):
            try:
                with urllib.request.urlopen(base + "/readyz", timeout=1) as response:
                    if response.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError("core did not become ready: " + run("docker", "logs", container))
        for _ in range(3):
            req = urllib.request.Request(base + "/release-smoke", data=b'{}', method="POST")
            with urllib.request.urlopen(req, timeout=3) as response:
                if response.status != 200 or response.read() != b"release-smoke-ok":
                    raise RuntimeError("unexpected canary response")
        run("docker", "stop", "--time", "15", container)
        status = json.loads(run("docker", "inspect", container))[0]["State"]
        if status["ExitCode"] != 0:
            raise RuntimeError(f"unclean shutdown: {status['ExitCode']}")
        return {"requests": 3, "readiness": "passed", "shutdown_exit_code": 0}
    finally:
        subprocess.run(["docker", "rm", "-f", container], check=False, stdout=subprocess.DEVNULL)


def console_install(image, version, commit):
    container = run("docker", "run", "-d", "--platform", "linux/amd64", "-p", "127.0.0.1::8080",
                    "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m,mode=1777",
                    "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "128",
                    "-e", "REPLAY_API_URL=http://127.0.0.1:8081", image)
    try:
        port = run("docker", "port", container, "8080/tcp").rsplit(":", 1)[1]
        base = f"http://127.0.0.1:{port}"
        for attempt in range(100):
            try:
                with urllib.request.urlopen(base + "/healthz", timeout=1) as response:
                    if response.status == 200:
                        break
            except (OSError, urllib.error.URLError):
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError("console did not become ready: " + run("docker", "logs", container))
        with urllib.request.urlopen(base + "/build-info.json", timeout=3) as response:
            identity = json.load(response)
        if identity != {"version": version, "commit": commit}:
            raise RuntimeError(f"unexpected frontend build identity: {identity}")
        for path in ("/", "/builder", "/wasm_exec.js", "/hookway.wasm"):
            with urllib.request.urlopen(base + path, timeout=3) as response:
                body = response.read()
                if response.status != 200 or not body or (path.endswith('.wasm') and not body.startswith(b'\x00asm')):
                    raise RuntimeError(f"invalid frontend asset: {path}")
                if response.headers.get("Cache-Control") != "no-store" or response.headers.get("X-Content-Type-Options") != "nosniff":
                    raise RuntimeError(f"frontend asset lacks security/cache headers: {path}")
                if path.endswith('.wasm') and response.headers.get_content_type() != "application/wasm":
                    raise RuntimeError("frontend WASM has the wrong MIME type")
                csp = response.headers.get("Content-Security-Policy", "")
                if "connect-src 'self' http://127.0.0.1:8081" not in csp or "frame-ancestors 'none'" not in csp:
                    raise RuntimeError("frontend CSP does not restrict API/frame access")
        with urllib.request.urlopen(base + "/runtime-config.json", timeout=3) as response:
            if json.load(response) != {"replayApiUrl": "http://127.0.0.1:8081"}:
                raise RuntimeError("frontend runtime configuration differs from deployment")
        for path in ("/v1/me", "/validate", "/assets/missing.js", "/.env", "/compare", "/use-cases"):
            try:
                urllib.request.urlopen(base + path, timeout=3).close()
            except urllib.error.HTTPError as error:
                if error.code == 404:
                    continue
            raise RuntimeError(f"frontend exposed a non-frontend route: {path}")
        return {"build_info": identity, "static_assets": "passed", "health": "passed", "security_headers": "passed", "read_only_runtime": "passed", "api_and_missing_assets": "404"}
    finally:
        subprocess.run(["docker", "rm", "-f", container], check=False, stdout=subprocess.DEVNULL)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("core", "replay", "console", "version", "commit", "output"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--previous-core")
    parser.add_argument("--previous-replay")
    parser.add_argument("--previous-console")
    parser.add_argument("--previous-version")
    parser.add_argument("--previous-commit")
    args = parser.parse_args()
    previous = [args.previous_core, args.previous_replay, args.previous_console, args.previous_version, args.previous_commit]
    if any(previous) and not all(previous):
        parser.error("rollback needs all five --previous-* arguments")
    report = {"schema_version": 1, "candidate": {}, "rollback": "not_run", "replay_database": "not_exercised"}
    with tempfile.TemporaryDirectory(prefix="hookway-release-") as directory:
        config = pathlib.Path(directory) / "config.yaml"
        config.write_text('''server:
  addr: ":8080"
  max_body_bytes: 1048576
flows:
  smoke:
    nodes:
      - {id: in, type: http_trigger, path: /release-smoke, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: respond, status: 200, body: release-smoke-ok}
    edges: [in>body, body>auth, auth>env, env>out]
''')
        for service in ("core", "replay", "console"):
            report["candidate"][service] = checked_image(getattr(args, service), args.version, args.commit, static=service == "console")
        report["candidate"]["install"] = core_install(args.core, config)
        report["candidate"]["restart"] = core_install(args.core, config)
        if all(previous):
            report["rollback"] = {}
            for service in ("core", "replay", "console"):
                report["rollback"][service] = checked_image(getattr(args, "previous_" + service), args.previous_version, args.previous_commit, static=service == "console")
            report["rollback"]["install"] = core_install(args.previous_core, config)
    pathlib.Path(args.output).write_text(json.dumps(report, indent=2) + "\n")


if __name__ == "__main__":
    main()
