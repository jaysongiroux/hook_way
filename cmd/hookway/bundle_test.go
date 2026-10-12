package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateBundleCLIModes(t *testing.T) {
	path := "../../examples/multi-yaml/core.yaml"
	for _, flags := range [][]string{{}, {"--json"}, {"--no-env"}, {"--json", "--no-env"}, {"--strict"}, {"--json", "--strict"}} {
		args := append([]string{"validate", "--config", path}, flags...)
		if code, stdout, stderr := runCLI(args...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, stdout, stderr)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "core.yaml"), []byte("flows: {orders: {file: orders.yaml}}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{}, {"--json"}, {"--no-env"}, {"--json", "--no-env"}} {
		args := append([]string{"validate", "--config", filepath.Join(dir, "core.yaml")}, flags...)
		if code, stdout, stderr := runCLI(args...); code == 0 || !strings.Contains(stdout+stderr, "orders.yaml") {
			t.Fatalf("%v: %d %s %s", args, code, stdout, stderr)
		}
	}
	if code, stdout, stderr := runCLI("serve", "--config", filepath.Join(dir, "core.yaml")); code == 0 || !strings.Contains(stdout+stderr, "orders.yaml") {
		t.Fatalf("startup did not reject missing file: %d %s", code, stderr)
	}
}

func TestLegacyCompactEnvironmentYAMLStillLoads(t *testing.T) {
	t.Setenv("LEGACY_BODY_LIMIT", "1048576")
	filename := filepath.Join(t.TempDir(), "core.yaml")
	// Before expansion the brace inside the flow mapping is invalid YAML.
	if err := os.WriteFile(filename, []byte("server: {addr: ':8080', max_body_bytes: ${LEGACY_BODY_LIMIT}}\nflows: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{}, {"--json"}} {
		args := append([]string{"validate", "--config", filename}, flags...)
		if code, out, stderr := runCLI(args...); code != 0 {
			t.Fatalf("legacy inline env behavior changed: %d %s %s", code, out, stderr)
		}
	}
}
