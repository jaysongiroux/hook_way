package flowconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestResolveFlowFilesEquivalentToInline(t *testing.T) {
	filename := "../../examples/multi-yaml/core.yaml"
	doc, issues := ResolveFile(filename)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	split, issues := Load(filename)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	inline, issues := LoadBytes(doc.Data, "inline", true)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	if !reflect.DeepEqual(split, inline) {
		t.Fatal("split and assembled inline configs differ")
	}
	if len(split.Flows) != 2 || !strings.HasSuffix(doc.FlowSources["orders"], "/flows/orders.yaml") {
		t.Fatal(doc.FlowSources)
	}
}

func TestResolveRejectsInvalidLayouts(t *testing.T) {
	tests := []struct{ name, entry, body, want string }{
		{"mixed", "flows: {a: {file: a.yaml}, b: {nodes: []}}", "nodes: []", "cannot be mixed"},
		{"inline fields", "flows: {a: {file: a.yaml, nodes: []}}", "nodes: []", "inline flow fields"},
		{"duplicate", "flows: {a: {file: a.yaml}, b: {file: ./a.yaml}}", "nodes: []", "multiple flows"},
		{"wrapper", "flows: {a: {file: a.yaml}}", "flows: {a: {nodes: []}}", "only flow fields"},
		{"central", "flows: {a: {file: a.yaml}}", "server: {}", "only flow fields"},
		{"nested", "flows: {a: {file: a.yaml}}", "file: b.yaml", "only flow fields"},
		{"multi-document", "flows: {a: {file: a.yaml}}", "nodes: []\n---\nnodes: []", "multiple YAML documents"},
		{"duplicate keys", "flows: {a: {file: a.yaml}, a: {file: b.yaml}}", "nodes: []", "already defined"},
		{"bad syntax", "flows: {a: {file: a.yaml}}", "nodes: [", "expected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, issues := Resolve([]byte(tt.entry), "core.yaml", func(string) ([]byte, error) { return []byte(tt.body), nil })
			if len(issues) == 0 || !strings.Contains(issues[0].Message, tt.want) {
				t.Fatalf("want %s, got %+v", tt.want, issues)
			}
		})
	}
	for _, filename := range []string{"/a.yaml", "../a.yaml", "x/../../a.yaml", "https://example.com/a.yaml", "a/*.yaml", "${FILE}.yaml", "a.txt", `C:\a.yaml`} {
		if _, err := YAMLPath(filename); err == nil {
			t.Errorf("accepted %s", filename)
		}
	}
}

func TestByteValidationNeverReadsFlowFiles(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "flow.yaml")
	if err := os.WriteFile(filename, []byte("nodes: []"), 0600); err != nil {
		t.Fatal(err)
	}
	_, issues := LoadBytes([]byte("flows: {a: {file: flow.yaml}}"), filepath.Join(filepath.Dir(filename), "core.yaml"), false)
	if len(issues) == 0 || !strings.Contains(issues[0].Message, "not supplied") {
		t.Fatal(issues)
	}
}

func TestFilesystemConfinementAndAliases(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("nodes: []"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.yaml"), []byte("flows: {a: {file: a.yaml}}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "a.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, issues := Load(filepath.Join(dir, "core.yaml")); len(issues) == 0 {
		t.Fatal("accepted escaping symlink")
	}
	if err := os.Remove(filepath.Join(dir, "a.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("nodes: []"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.yaml", filepath.Join(dir, "b.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "core.yaml"), []byte("flows: {a: {file: a.yaml}, b: {file: b.yaml}}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, issues := Load(filepath.Join(dir, "core.yaml")); len(issues) == 0 || !strings.Contains(issues[0].Message, "multiple flows") {
		t.Fatal(issues)
	}
}

func TestSplitCrossFlowIssuesAndFileSources(t *testing.T) {
	body, err := os.ReadFile("../../examples/multi-yaml/flows/orders.yaml")
	if err != nil {
		t.Fatal(err)
	}
	entry := []byte("flows: {a: {file: a.yaml}, b: {file: b.yaml}}")
	doc, issues := Resolve(entry, "core.yaml", func(string) ([]byte, error) { return body, nil })
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	_, issues = LoadBytes(doc.Data, "core.yaml", false)
	issues = doc.Attribute(issues)
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "path") {
			found = true
			if issue.Source == "" {
				t.Fatal(issue)
			}
		}
	}
	if !found {
		t.Fatal("cross-flow path conflict was not validated", issues)
	}
	invalid := strings.Replace(string(body), "status: 202", "status: 999", 1)
	doc, _ = Resolve([]byte("flows: {orders: {file: flows/orders.yaml}}"), "core.yaml", func(string) ([]byte, error) { return []byte(invalid), nil })
	_, issues = LoadBytes(doc.Data, "core.yaml", false)
	for _, issue := range doc.Attribute(issues) {
		if strings.HasPrefix(issue.Field, "flows.orders.") && issue.Source != "flows/orders.yaml" {
			t.Fatal(issue)
		}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(doc.Data, &parsed); err != nil {
		t.Fatal(err)
	}
}
