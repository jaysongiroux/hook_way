package flowconfig

import (
	"strings"
	"testing"
)

func TestLookupProviderValidation(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		valid        bool
	}{
		{"http", `http: {url: 'https://lookup.example.com/{key}', value_path: tenant}`, true},
		{"internal explicit", `http: {url: 'http://127.0.0.1/{key}', value_path: tenant, allow_private_targets: true}`, true},
		{"internal denied", `http: {url: 'http://127.0.0.1/{key}', value_path: tenant}`, false},
		{"authority injection", `http: {url: 'https://{key}.example.com/value', value_path: tenant}`, false},
		{"userinfo", `http: {url: 'https://secret@example.com/{key}', value_path: tenant}`, false},
		{"postgres env", `postgres: {url: "${DATABASE_URL}", query: 'SELECT env FROM routes WHERE id = {key}'}`, true},
		{"http env", `http: {url: "${DIRECTORY_BASE}/{key}", value_path: tenant}`, true},
		{"postgres", `postgres: {url: 'postgres://localhost/routes', query: 'SELECT env FROM routes WHERE id = {key}'}`, true},
		{"bad query", `postgres: {url: 'postgres://localhost/routes', query: 'SELECT env FROM routes'}`, false},
		{"dynamo", `dynamodb: {table: routes, value_attribute: env}`, true},
		{"two providers", "http: {url: 'https://example.com/{key}', value_path: env}\n    dynamodb: {table: routes, value_attribute: env}", false},
		{"unknown", `htp: {url: 'https://example.com/{key}'}`, false},
		{"bad timeout", "timeout: 31s\n    dynamodb: {table: routes, value_attribute: env}", false},
		{"zero cache", "cache: {max_entries: 0}\n    dynamodb: {table: routes, value_attribute: env}", false},
		{"negative TTL", "cache: {negative_ttl: -1s}\n    dynamodb: {table: routes, value_attribute: env}", false},
		{"duplicate secret header", "http:\n      url: 'https://example.com/{key}'\n      value_path: env\n      headers: {Authorization: literal}\n      secret_headers: {authorization: {secret_ref: {provider: env, name: TOKEN}}}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := LoadBytes([]byte("lookups:\n  routes:\n    "+tc.config), "test", false)
			var found bool
			for _, issue := range issues {
				if strings.HasPrefix(issue.Field, "lookups") {
					found = true
				}
			}
			if found == tc.valid {
				t.Fatalf("valid=%v issues=%+v", tc.valid, issues)
			}
		})
	}
}

func TestStandaloneLookupReferencesAndOverrides(t *testing.T) {
	source := `server: {addr: ":8080", max_body_bytes: 1048576}
lookups:
  routes:
    http: {url: 'https://lookup.example.com/{key}', value_path: tenant}
flows:
  demo:
    nodes:
    - {id: in, type: http_trigger, path: /demo, methods: [POST]}
    - {id: body, type: read_body, max_bytes: 1048576}
    - {id: auth, type: no_auth}
    - {id: env, type: envelope}
    - {id: route, type: lookup, provider: routes, key: '{body.id}'}
    - {id: out, type: respond, status: 200}
    edges: [in>body,body>auth,auth>env,env>route,route>out]
`
	cfg, issues := LoadBytes([]byte(source), "test", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if issues := StandaloneIssues(cfg); len(issues) > 0 {
		t.Fatal(issues)
	}
	cfg, issues = LoadBytes([]byte(strings.Replace(source, "provider: routes", "provider: absent", 1)), "test", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if len(StandaloneIssues(cfg)) != 1 {
		t.Fatal("missing provider not rejected")
	}
	cfg, _ = LoadBytes([]byte(strings.Replace(source, "provider: routes", "provider: routes, table: legacy", 1)), "test", false)
	if len(StandaloneIssues(cfg)) != 1 {
		t.Fatal("ignored override not rejected")
	}
}
