package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCORSOriginsAreExactAndResolvedOnlyAtRuntime(t *testing.T) {
	for _, origin := range []string{"https://console.example.test", "http://127.0.0.1:5183", "http://localhost:3000", "http://[::1]:5183"} {
		doc := minimalYAML + fmt.Sprintf("server: {cors: {allowed_origins: [%q]}}\n", origin)
		cfg, issues := LoadBytes([]byte(doc))
		if len(issues) != 0 || cfg.Server.CORS.AllowedOrigins[0] != origin {
			t.Fatalf("%s: %+v", origin, issues)
		}
	}
	for _, origin := range []string{"*", "null", "https://*.example.test", "https://console.example.test/", "https://console.example.test/app", "https://console.example.test?", "https://console.example.test#", "https://user@console.example.test", "ftp://console.example.test", " https://console.example.test", "https://console.example.test:443", "https://CONSOLE.example.test", "https://console.example.test:70000", "https://console.example.test:0", "http://[::1%25eth0]:5183", "https://console.example.test,https://evil.test"} {
		doc := minimalYAML + fmt.Sprintf("server: {cors: {allowed_origins: [%q]}}\n", origin)
		_, issues := LoadBytes([]byte(doc))
		if len(errorsOnly(issues)) == 0 {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	doc := minimalYAML + "server: {cors: {allowed_origins: ['${REPLAY_CONSOLE_ORIGIN}']}}\n"
	t.Setenv("REPLAY_CONSOLE_ORIGIN", "null")
	cfg, issues := ValidateBytes([]byte(doc))
	if len(issues) != 0 || cfg.Server.CORS.AllowedOrigins[0] != "${REPLAY_CONSOLE_ORIGIN}" {
		t.Fatalf("pure validation expanded ref: %+v %+v", cfg.Server, issues)
	}
	if _, issues := LoadBytes([]byte(doc)); len(errorsOnly(issues)) == 0 {
		t.Fatal("runtime accepted resolved null origin")
	}
	t.Setenv("REPLAY_CONSOLE_ORIGIN", "https://console.example.test")
	if _, issues := LoadBytes([]byte(doc)); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestReplayConfigRejectsEmbeddedConsoleAndLimitsTokensByBytes(t *testing.T) {
	if _, issues := LoadBytes([]byte(minimalYAML + "console: {enabled: true}\n")); len(errorsOnly(issues)) == 0 {
		t.Fatal("embedded console config accepted")
	}
	schema, err := SchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	var shape struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &shape); err != nil {
		t.Fatal(err)
	}
	if _, exists := shape.Properties["console"]; exists {
		t.Fatal("schema advertises embedded console")
	}
	for _, token := range []string{strings.Repeat("a", 257), strings.Repeat("é", 129)} {
		for _, original := range []string{captureToken, controlToken} {
			_, issues := LoadBytes([]byte(strings.Replace(minimalYAML, original, token, 1)))
			if len(errorsOnly(issues)) == 0 {
				t.Fatal("oversized token accepted")
			}
		}
	}
	if _, issues := LoadBytes([]byte(strings.Replace(minimalYAML, captureToken, strings.Repeat("a", 256), 1))); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestFormerConsolePathsAreAvailableForAuthenticatedMetrics(t *testing.T) {
	for _, path := range []string{"/console", "/console/", "/console/metrics"} {
		doc := strings.Replace(validYAML, `path: /metrics, addr: ":9090"`, `path: `+path, 1)
		if _, issues := LoadBytes([]byte(doc)); len(issues) != 0 {
			t.Fatalf("%s: %+v", path, issues)
		}
	}
}
