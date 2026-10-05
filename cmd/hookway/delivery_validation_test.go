package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDeliveryWarningsRemainOptInStrict(t *testing.T) {
	src := `state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1048576}
destinations:
  notify: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  orders:
    nodes:
      - {id: in, type: http_trigger, path: /orders, methods: [POST]}
      - {id: limit, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: test, signature_header: X-Signature}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: notify, target: orders}
    edges: [in>limit, limit>body, body>auth, auth>env, env>out]
`
	path := filepath.Join(t.TempDir(), "delivery.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, strict := range []bool{false, true} {
		args := []string{"validate", "--config", path}
		if strict {
			args = append(args, "--strict")
		}
		var stdout, stderr bytes.Buffer
		code := run(args, &stdout, &stderr)
		if (code != 0) != strict {
			t.Fatalf("strict %v: code %d, %s %s", strict, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String()+stderr.String(), "in-memory queue") {
			t.Fatalf("strict %v: missing warning: %s %s", strict, stdout.String(), stderr.String())
		}
	}
}
