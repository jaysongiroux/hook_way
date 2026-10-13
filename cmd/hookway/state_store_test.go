package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateStoreHelpAndUsage(t *testing.T) {
	for _, args := range [][]string{{"state-store", "-h"}, {"help", "state-store"}} {
		var out, err bytes.Buffer
		if code := run(args, &out, &err); code != 0 || err.Len() != 0 || !strings.Contains(out.String(), "expected-fingerprint") {
			t.Fatal(code, out.String(), err.String())
		}
	}
	t.Setenv("HOOKWAY_CONFIG", "")
	for _, args := range [][]string{{"state-store"}, {"state-store", "status"}, {"state-store", "status", "--store", "test"}, {"state-store", "policy-update", "--config", "test", "--store", "pg", "--policy", "quota"}, {"state-store", "gc", "--config", "test", "--store", "pg", "--timeout", "1ns"}} {
		var out, err bytes.Buffer
		if code := run(args, &out, &err); code != 2 {
			t.Fatal(args, code, out.String(), err.String())
		}
	}
}
func TestStateStoreMemoryAdministrationRejectedAndEnvConfigResolves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.yaml")
	if err := os.WriteFile(path, []byte(`server: {addr: ':8080', max_body_bytes: 1024}
state_stores: {local: {namespace: example, memory: {}}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: ok, type: respond, status: 200}
    edges: [in>body, body>auth, auth>env, env>ok]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOKWAY_CONFIG", path)
	var out, err bytes.Buffer
	if code := run([]string{"state-store", "status", "--store", "local"}, &out, &err); code != 1 || !strings.Contains(err.String(), "private to the serving process") {
		t.Fatal(code, out.String(), err.String())
	}
}
