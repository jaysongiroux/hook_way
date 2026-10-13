package acceptance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

// Exercise the documented add/migrate/remove/restart sequence with independent
// native processes. Config-file edits alone must not revoke a running key.
func TestAcceptance_NamedKeyRotationAcrossReplayReplicas(t *testing.T) {
	_, replayBin := binaries(t)
	const newToken = "new-operator-marker-0123456789abcdef"
	const readOnlyToken = "read-only-marker-0123456789abcdefghi"
	const metricsToken = "metrics-only-marker-0123456789abcdef" // #nosec G101 -- synthetic test-only token, never a deployment credential
	const wrongToken = "wrong-key-marker-0123456789abcdefghi"
	env := []string{
		"HOOKWAY_ROTATION_TEST_DATABASE_URL=" + replaytest.Database(t),
		"HOOKWAY_ROTATION_TEST_CONTROL_TOKEN=" + controlToken,
		"HOOKWAY_ROTATION_TEST_CAPTURE_TOKEN=" + captureToken,
		"HOOKWAY_ROTATION_TEST_OLD_TOKEN=" + operatorToken,
		"HOOKWAY_ROTATION_TEST_NEW_TOKEN=" + newToken,
		"HOOKWAY_ROTATION_TEST_READ_TOKEN=" + readOnlyToken,
		"HOOKWAY_ROTATION_TEST_METRICS_TOKEN=" + metricsToken,
	}
	dir := t.TempDir()
	addrs := []string{freeAddr(t), freeAddr(t)}
	paths := []string{filepath.Join(dir, "replay-0.yaml"), filepath.Join(dir, "replay-1.yaml")}
	source := func(addr, stage string) string {
		text := fmt.Sprintf(`server: {addr: %q}
metrics: {enabled: true, path: /metrics}
database: {url: "${HOOKWAY_ROTATION_TEST_DATABASE_URL}", max_conns: 3}
core: {control_url: "http://127.0.0.1:1", control_token: "${HOOKWAY_ROTATION_TEST_CONTROL_TOKEN}"}
keys:
  - {name: core, token: "${HOOKWAY_ROTATION_TEST_CAPTURE_TOKEN}", scopes: [capture]}
  - {name: observer, token: "${HOOKWAY_ROTATION_TEST_READ_TOKEN}", scopes: [read]}
  - {name: prometheus, token: "${HOOKWAY_ROTATION_TEST_METRICS_TOKEN}", scopes: [metrics]}
`, addr)
		if stage != "new" {
			text += "  - {name: operator-old, token: \"${HOOKWAY_ROTATION_TEST_OLD_TOKEN}\", scopes: [read, payload, retry]}\n"
		}
		if stage != "old" {
			text += "  - {name: operator-new, token: \"${HOOKWAY_ROTATION_TEST_NEW_TOKEN}\", scopes: [read, payload, retry]}\n"
		}
		return text
	}
	for i := range paths {
		writeConfig(t, paths[i], source(addrs[i], "old"))
	}
	migrate := exec.CommandContext(t.Context(), replayBin, "migrate", "--config", paths[0]) // #nosec G204 -- repository-built binary and test-owned config
	migrate.Env = append(os.Environ(), env...)
	if out, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("migration failed: %v\n%s", err, out)
	}
	var history []*process
	startReplica := func(i int, stage string) *process {
		p := startProcess(t, fmt.Sprintf("rotation-replay-%d-%s", i, stage), replayBin, env, "serve", "--config", paths[i])
		history = append(history, p)
		waitReady(t, p.name, "http://"+addrs[i]+"/readyz")
		return p
	}
	processes := []*process{startReplica(0, "old"), startReplica(1, "old")}
	checkIdentity := func(i int, token, actor string, want int) {
		t.Helper()
		status, data, err := get("http://"+addrs[i]+"/v1/me", "Authorization", "Bearer "+token)
		if err != nil || status != want {
			t.Fatalf("replica %d actor %s: status %d, want %d, err %v", i, actor, status, want, err)
		}
		for _, marker := range []string{operatorToken, newToken, readOnlyToken, metricsToken, wrongToken, captureToken, controlToken} {
			if strings.Contains(string(data), marker) {
				t.Fatalf("replica %d response leaked a credential", i)
			}
		}
		if want == http.StatusOK {
			var identity struct {
				Name   string   `json:"key_name"`
				Scopes []string `json:"scopes"`
			}
			if err := json.Unmarshal(data, &identity); err != nil || identity.Name != actor {
				t.Fatalf("replica %d identity %q, want %s, err %v", i, identity.Name, actor, err)
			}
			if actor == "operator-old" || actor == "operator-new" {
				if strings.Join(identity.Scopes, ",") != "read,payload,retry" {
					t.Fatalf("operator scopes changed: %v", identity.Scopes)
				}
			}
		}
	}
	for i := range processes {
		checkIdentity(i, operatorToken, "operator-old", http.StatusOK)
		checkIdentity(i, newToken, "operator-new", http.StatusUnauthorized)
		checkIdentity(i, wrongToken, "", http.StatusUnauthorized)
		checkIdentity(i, captureToken, "core", http.StatusForbidden)
		checkIdentity(i, metricsToken, "prometheus", http.StatusForbidden)
		checkIdentity(i, readOnlyToken, "observer", http.StatusOK)
	}
	// Installing the overlap config does not change live authenticators.
	for i := range processes {
		writeConfig(t, paths[i], source(addrs[i], "overlap"))
		checkIdentity(i, newToken, "operator-new", http.StatusUnauthorized)
	}
	for i := range processes {
		processes[i].stop()
		processes[i] = startReplica(i, "overlap")
		checkIdentity(i, newToken, "operator-new", http.StatusOK)
		for j := range processes {
			checkIdentity(j, operatorToken, "operator-old", http.StatusOK)
		}
	}
	// Removing the old key also takes effect only after each replica restarts.
	for i := range processes {
		writeConfig(t, paths[i], source(addrs[i], "new"))
		checkIdentity(i, operatorToken, "operator-old", http.StatusOK)
	}
	for i := range processes {
		processes[i].stop()
		processes[i] = startReplica(i, "new")
		checkIdentity(i, operatorToken, "operator-old", http.StatusUnauthorized)
		checkIdentity(i, newToken, "operator-new", http.StatusOK)
		if i == 0 {
			checkIdentity(1, operatorToken, "operator-old", http.StatusOK)
		}
	}
	for i := range processes {
		// Read-only keys cannot mutate; operator keys cannot scrape private metrics.
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addrs[i]+"/v1/retries", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+readOnlyToken)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("read-only mutation = %d", resp.StatusCode)
		}
		for _, tc := range []struct {
			token string
			want  int
		}{{newToken, http.StatusForbidden}, {captureToken, http.StatusForbidden}, {metricsToken, http.StatusOK}} {
			status, _, err := get("http://"+addrs[i]+"/metrics", "Authorization", "Bearer "+tc.token)
			if err != nil || status != tc.want {
				t.Fatalf("metrics scope = %d, want %d, err %v", status, tc.want, err)
			}
		}
		// Duplicate credentials remain invalid after every rotation stage.
		req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addrs[i]+"/v1/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header["Authorization"] = []string{"Bearer " + newToken, "Bearer " + newToken}
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("duplicate operator authorization = %d", resp.StatusCode)
		}
	}
	for _, p := range history {
		p.stop()
		if !p.cmd.ProcessState.Success() {
			t.Fatalf("%s failed shutdown", p.name)
		}
		for _, marker := range []string{operatorToken, newToken, readOnlyToken, metricsToken, wrongToken, captureToken, controlToken} {
			if strings.Contains(p.out.String(), marker) {
				t.Fatalf("%s logs leaked a credential", p.name)
			}
		}
	}
}
