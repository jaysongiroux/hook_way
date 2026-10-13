"""Exercise the frontend startup security boundary without Docker."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1] / "runtime_config.py"
spec = importlib.util.spec_from_file_location("ui_configure", SOURCE)
configure = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configure)


class UIHostingTests(unittest.TestCase):
    def test_urls_normalize_origin_without_losing_deployment_prefix(self):
        for raw, expected in (
            ("https://API.example:443/replay/", ("https://api.example/replay", "https://api.example")),
            ("http://localhost:8081", ("http://localhost:8081", "http://localhost:8081")),
            ("http://[::1]:8081/replay", ("http://[::1]:8081/replay", "http://[::1]:8081")),
            ("https://api.example/v1", ("https://api.example/v1", "https://api.example")),
        ):
            with self.subTest(raw=raw):
                self.assertEqual(configure.api_url(raw), expected)

    def test_missing_malformed_or_injected_urls_are_rejected(self):
        for raw in (
            "", "/api", "ftp://api.example", "http://user:secret@api.example",
            "https://api.example?token=secret", "https://api.example#fragment",
            "https://api.example?", "https://api.example#", " https://api.example",
            "https://api.example\nadd_header X secret", "https://api.example\\other",
            'https://api.example"; proxy_pass http://evil.example;',
            "http://[bad]:8081", "http://[::1%eth0]", "http://localhost:0",
            "http://localhost:65536", "http://localhost:secret", "http://999.1.1.1",
            "http://api..example", "https://api.example/../other", "https://api.example/%2e%2e/other",
            "https://api.example/%GG", "https://api.example/" + "x" * 2048,
        ):
            with self.subTest(raw=raw):
                with self.assertRaises(ValueError):
                    configure.api_url(raw)

    def test_runtime_json_and_static_only_headers_are_generated(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = Path(temp)
            configure.configure("https://api.example/replay", directory, Path("/usr/share/nginx/html"))
            self.assertEqual(json.loads((directory / "runtime-config.json").read_text()), {"replayApiUrl": "https://api.example/replay"})
            nginx = (directory / "nginx.conf").read_text()
            self.assertIn("connect-src 'self' https://api.example", nginx)
            self.assertIn('Cache-Control "no-store" always', nginx)
            self.assertIn("location = /builder { try_files /console.html =404; }", nginx)
            self.assertIn("location / { try_files $uri =404; }", nginx)
            self.assertIn("frame-ancestors 'none'", nginx)
            self.assertNotIn("proxy_pass", nginx)
            self.assertNotIn("https://api.example/replay", nginx)

    def test_invalid_url_errors_never_echo_a_supplied_secret(self):
        for raw in ("https://user:secret@api.example", "https://api.example:secret", "https://[secret]"):
            env = dict(os.environ, REPLAY_API_URL=raw)
            result = subprocess.run([sys.executable, str(SOURCE)], env=env, capture_output=True, text=True, check=False)
            self.assertEqual(result.returncode, 1)
            self.assertNotIn("secret", result.stderr)


if __name__ == "__main__":
    unittest.main()
