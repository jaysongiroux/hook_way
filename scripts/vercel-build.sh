#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
required_go="go1.26.8"

# Vercel's Node build image does not promise the Go toolchain used by go.mod.
# Use an existing exact match, or download and verify the official archive.
if command -v go >/dev/null 2>&1 && [ "$(go version | awk '{print $3}')" = "$required_go" ]; then
  go_bin="$(command -v go)"
else
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)
      archive="go1.26.8.linux-amd64.tar.gz"
      checksum="d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b"
      ;;
    Linux-aarch64)
      archive="go1.26.8.linux-arm64.tar.gz"
      checksum="211ffced9dcb9633a55eac6364816ec0ddd951389a740e88fa8b3337971bdda0"
      ;;
    *)
      echo "Go $required_go is required to build the browser validator" >&2
      exit 1
      ;;
  esac
  tool_dir="$(mktemp -d)"
  trap 'rm -rf "$tool_dir"' EXIT
  curl --fail --location --silent --show-error --retry 3 "https://go.dev/dl/$archive" -o "$tool_dir/$archive"
  printf '%s  %s\n' "$checksum" "$tool_dir/$archive" | sha256sum --check --status
  tar -C "$tool_dir" -xzf "$tool_dir/$archive"
  go_bin="$tool_dir/go/bin/go"
fi

cd "$repo_root"
make GO="$go_bin" wasm web-build
test -s web/dist/hookway.wasm
test -s web/dist/wasm_exec.js
