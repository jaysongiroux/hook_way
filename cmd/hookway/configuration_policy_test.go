package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/configapi"
)

func TestValidateConfigurationPolicyStrictIsOptional(t *testing.T) {
	src := `server: {addr: ':8080', max_body_bytes: 1024, max_inflight_requests: -1}
state_stores: {test: {namespace: tests, memory: {}}}
security: {trusted_proxies: ['0.0.0.0/0']}
flows:
  local:
    nodes:
      - {id: in, type: http_trigger, path: /local, methods: [POST]}
      - {id: rl, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: '${HOOKWAY_POLICY_TEST_SECRET}', signature_header: X-Signature}
      - {id: env, type: envelope}
      - {id: reply, type: respond, status: 200}
    edges: [in>rl, rl>body, body>auth, auth>env, env>reply]
`
	t.Setenv("HOOKWAY_POLICY_TEST_SECRET", "change-me")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, jsonOutput := range []bool{false, true} {
		for _, strict := range []bool{false, true} {
			for _, noEnv := range []bool{false, true} {
				args := []string{"validate", "--config", path}
				if jsonOutput {
					args = append(args, "--json")
				}
				if strict {
					args = append(args, "--strict")
				}
				if noEnv {
					args = append(args, "--no-env")
				}
				var stdout, stderr bytes.Buffer
				code := run(args, &stdout, &stderr)
				if (code != 0) != strict {
					t.Fatalf("json=%v strict=%v no-env=%v: exit %d, output %s %s", jsonOutput, strict, noEnv, code, stdout.String(), stderr.String())
				}
				output := stdout.String() + stderr.String()
				if strings.Contains(output, "change-me") {
					t.Fatalf("sample credential leaked: %s", output)
				}
				if !strings.Contains(output, "finite max_inflight_requests") {
					t.Fatalf("correction missing: %s", output)
				}
				if strings.Contains(output, "known sample credential") == noEnv {
					t.Fatalf("sample detection ignored env policy: %s", output)
				}
				if jsonOutput {
					var response configapi.ValidateResponse
					if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					for _, issue := range response.Issues {
						if issue.Severity != "warning" || issue.Suggestion == "" {
							t.Fatalf("unexpected issue: %#v", issue)
						}
					}
				}
			}
		}
	}
}
