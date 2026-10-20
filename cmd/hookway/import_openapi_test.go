package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportOpenAPICLI(t *testing.T) {
	spec := "../../examples/openapi/api.yaml"
	settings := "../../examples/openapi/settings.yaml"
	for _, args := range [][]string{{"--spec", spec, "--list"}, {"--spec", spec, "--list", "--json"}, {"--spec", spec, "--settings", settings}} {
		var out, err bytes.Buffer
		code := run(append([]string{"import-openapi"}, args...), &out, &err)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, err.String())
		}
		if out.Len() == 0 {
			t.Fatal("empty output")
		}
		if strings.Contains(strings.Join(args, " "), "--json") {
			var report map[string]any
			if e := json.Unmarshal(out.Bytes(), &report); e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, args := range [][]string{{}, {"--spec", spec, "--json"}, {"--spec", spec, "--list", "--settings", settings}, {"--spec", spec, "unexpected"}, {"--unknown"}} {
		var out, err bytes.Buffer
		if code := runImportOpenAPI(args, &out, &err); code != 2 {
			t.Fatalf("usage %v: %d %s", args, code, err.String())
		}
		if out.Len() > 0 {
			t.Fatal("usage error wrote YAML")
		}
	}
	output := filepath.Join(t.TempDir(), "core.yaml")
	args := []string{"--spec", spec, "--settings", settings, "--out", output}
	var out, err bytes.Buffer
	if runImportOpenAPI(args, &out, &err) != 0 {
		t.Fatal(err.String())
	}
	before, e := os.ReadFile(output) // #nosec G304 -- test-owned path under t.TempDir.
	if e != nil {
		t.Fatal(e)
	}
	if runImportOpenAPI(args, &out, &err) != 1 {
		t.Fatal("overwrote output")
	}
	after, _ := os.ReadFile(output) // #nosec G304 -- test-owned path under t.TempDir.
	if !bytes.Equal(before, after) {
		t.Fatal("modified existing output")
	}
	bad := filepath.Join(t.TempDir(), "invalid.yaml")
	if e := os.WriteFile(bad, []byte("openapi: 2.0.0"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--spec", bad, "--settings", settings}, {"--spec", "/does-not-exist", "--list"}, {"--spec", bad, "--list", "--json"}} {
		var out, err bytes.Buffer
		if runImportOpenAPI(args, &out, &err) != 1 {
			t.Fatalf("expected failure for %v", args)
		}
	}
}
