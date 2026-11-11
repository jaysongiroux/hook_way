#!/usr/bin/env bash
# Build the browser validator for Vercel with the Go version pinned by go.mod.
# Invocation: bash scripts/vercel-build.sh
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
required_go="go1.27.4"

# Vercel's Node build image does not promise the Go toolchain used by go.mod.
# Use an existing exact match, or download and verify the official archive.
if command -v go >/dev/null 2>&1 && [ "$(go version | awk '{print $3}')" = "$required_go" ]; then
  go_bin="$(command -v go)"
else
  case "$(uname -s)-$(uname -m)" in
    Linux-x86_64)
      archive="go1.27.4.linux-amd64.tar.gz"
      checksum="7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840"
      ;;
    Linux-aarch64)
      archive="go1.27.4.linux-arm64.tar.gz"
      checksum="3392582988a273a87ab5043ee1b2c9c17a2c3c00580bf9326c47a2e0aaaeac92"
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
make GO="$go_bin" wasm website-build
test -s web/dist/hookway.wasm
test -s web/dist/wasm_exec.js
