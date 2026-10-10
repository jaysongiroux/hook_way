package configapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testBundle(t *testing.T) BundleInput {
	t.Helper()
	bundle := BundleInput{Entry: "core.yaml", Files: map[string]string{}}
	for _, name := range []string{"core.yaml", "flows/orders.yaml", "flows/billing.yaml"} {
		data, err := os.ReadFile("../../examples/multi-yaml/" + name)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Files[name] = string(data)
	}
	return bundle
}

func TestBundleValidationAndEnvironmentIsolation(t *testing.T) {
	bundle := testBundle(t)
	if response := ValidateBundle(bundle); len(response.Issues) != 0 {
		t.Fatal(response)
	}
	t.Setenv("BUNDLE_SECRET", "server-side-value")
	bundle.Files["flows/orders.yaml"] = strings.Replace(bundle.Files["flows/orders.yaml"], "change-me-demo-token", "${BUNDLE_SECRET}", 1)
	response := ValidateBundle(bundle)
	for _, issue := range response.Issues {
		if strings.Contains(issue.Message, "server-side-value") {
			t.Fatal("host environment leaked", response)
		}
	}
	bundle.Files["flows/orders.yaml"] = strings.Replace(bundle.Files["flows/orders.yaml"], "status: 202", "status: 999", 1)
	response = ValidateBundle(bundle)
	if len(response.Issues) == 0 {
		t.Fatal("invalid status accepted")
	}
	for _, issue := range response.Issues {
		if strings.HasPrefix(issue.Field, "flows.orders.") && issue.Source != "flows/orders.yaml" {
			t.Fatal(issue)
		}
	}
	delete(bundle.Files, "flows/orders.yaml")
	if response := ValidateBundle(bundle); len(response.Issues) == 0 || !strings.Contains(response.Issues[0].Message, "not supplied") {
		t.Fatal(response)
	}
}

func TestBundleRelativeEntryAndLimits(t *testing.T) {
	bundle := testBundle(t)
	relocated := BundleInput{Entry: "config/core.yaml", Files: map[string]string{}}
	for name, content := range bundle.Files {
		relocated.Files["config/"+name] = content
	}
	if response := ValidateBundle(relocated); len(response.Issues) != 0 {
		t.Fatal(response)
	}
	bundle.Files["./core.yaml"] = bundle.Files["core.yaml"]
	if response := ValidateBundle(bundle); len(response.Issues) == 0 {
		t.Fatal("duplicate normalized path accepted")
	}
	delete(bundle.Files, "./core.yaml")
	bundle.Files["flows/orders.yaml"] = strings.Repeat("x", MaxBundleDocumentBytes+1)
	if response := ValidateBundle(bundle); len(response.Issues) == 0 {
		t.Fatal("oversized document accepted")
	}
	for _, raw := range []string{`{}`, `{"entry":"core.yaml","files":{},"unknown":1}`, `{"entry":"core.yaml","files":{}} {}`, `null`} {
		if _, err := DecodeBundleInput([]byte(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}

func TestCLIJSONBundleEnvironmentModes(t *testing.T) {
	bundle := testBundle(t)
	bundle.Files["flows/orders.yaml"] = strings.Replace(bundle.Files["flows/orders.yaml"], "change-me-demo-token", "${MISSING_BUNDLE_VALUE}", 1)
	t.Setenv("MISSING_BUNDLE_VALUE", "")
	os.Unsetenv("MISSING_BUNDLE_VALUE")
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "flows"), 0700)
	for name, content := range bundle.Files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	filename := filepath.Join(dir, "core.yaml")
	for _, issue := range ValidateFileWithEnv(filename, true).Issues {
		if issue.Severity != "warning" {
			t.Fatal(issue)
		}
	}
	for _, issue := range ValidateFileWithEnv(filename, false).Issues {
		if strings.Contains(issue.Field, "config.env") {
			t.Fatal(issue)
		}
	}
}
