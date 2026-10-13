package configapi

import (
	"os"
	"strings"
	"testing"
)

func TestValidateMultipleDestinationInputs(t *testing.T) {
	data, err := os.ReadFile("../../examples/shared-destination.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []ValidateResponse{Validate(data), ValidateWithEnv(data, false), ValidateWithEnv(data, true)} {
		for _, issue := range out.Issues {
			if issue.Severity == "error" {
				t.Fatal(issue)
			}
		}
	}
	invalid := strings.Replace(string(data), "right>send", "right>left", 1)
	issues := Validate([]byte(invalid)).Issues
	found := false
	for _, issue := range issues {
		if issue.Severity == "error" && strings.Contains(issue.Message, "multiple incoming") {
			found = true
		}
	}
	if !found {
		t.Fatal("processing reconvergence accepted", issues)
	}
}
