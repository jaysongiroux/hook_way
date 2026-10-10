package configapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

const MaxBundleFiles = 1024
const MaxBundleDocumentBytes = 5 << 20
const MaxBundleBytes = 64 << 20
const MaxBundleRequestBytes = 128 << 20

// BundleInput contains deployment-relative filenames and raw YAML strings.
type BundleInput struct {
	Entry string            `json:"entry"`
	Files map[string]string `json:"files"`
}

// DecodeBundleInput is shared by HTTP and WASM; neither transport adds rules.
func DecodeBundleInput(raw []byte) (BundleInput, error) {
	var input BundleInput
	if len(raw) > MaxBundleRequestBytes {
		return input, errors.New("bundle request exceeds 128 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, errors.New("invalid bundle JSON")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return input, errors.New("request must contain one JSON object")
	}
	if input.Entry == "" || input.Files == nil {
		return input, errors.New("entry and files are required")
	}
	return input, nil
}

// ValidateBundle resolves only supplied YAML, never filesystem or environment.
func ValidateBundle(input BundleInput) ValidateResponse {
	failure := func(message string) ValidateResponse {
		return ValidateResponse{Issues: []Issue{{Field: "config", Message: message, Severity: "error", Source: input.Entry}}}
	}
	entry, err := flowconfig.YAMLPath(input.Entry)
	if err != nil {
		return failure("entry: " + err.Error())
	}
	if len(input.Files) > MaxBundleFiles {
		return failure("bundle exceeds 1024 files")
	}
	files := map[string]string{}
	total := 0
	for filename, content := range input.Files {
		canonical, err := flowconfig.YAMLPath(filename)
		if err != nil {
			return failure(filename + ": " + err.Error())
		}
		if _, exists := files[canonical]; exists {
			return failure("duplicate bundle path: " + canonical)
		}
		if len(content) > MaxBundleDocumentBytes {
			return failure(filename + " exceeds 5 MiB")
		}
		total += len(content)
		if total > MaxBundleBytes {
			return failure("bundle exceeds 64 MiB of YAML")
		}
		files[canonical] = content
	}
	data, ok := files[entry]
	if !ok {
		return failure("entry file is not supplied: " + entry)
	}
	doc, issues := flowconfig.Resolve([]byte(data), entry, func(filename string) ([]byte, error) {
		filename = path.Join(path.Dir(entry), filename)
		content, ok := files[filename]
		if !ok {
			return nil, fmt.Errorf("file is not supplied")
		}
		return []byte(content), nil
	})
	if len(issues) > 0 {
		return preserveSeverity(issues)
	}
	return attributeResponse(doc, Validate(doc.Data))
}

// ValidateFileWithEnv preserves CLI JSON validation's lenient missing-env policy
// after the same disk resolution used by serve and plain validate.
func ValidateFileWithEnv(filename string, expand bool) ValidateResponse {
	doc, issues := flowconfig.ResolveFile(filename)
	if expand && len(issues) == 1 && issues[0].Field == "config" {
		if raw, err := os.ReadFile(filename); err == nil && bytes.Contains(raw, []byte("$")) {
			response := ValidateWithEnv(raw, true)
			for i := range response.Issues {
				response.Issues[i].Source = filename
			}
			return response
		}
	}
	if len(issues) > 0 {
		return preserveSeverity(issues)
	}
	return attributeResponse(doc, ValidateWithEnv(doc.Data, expand))
}

func attributeResponse(doc flowconfig.ResolvedDocument, response ValidateResponse) ValidateResponse {
	issues := make([]config.ValidationIssue, len(response.Issues))
	for i, issue := range response.Issues {
		issues[i] = config.ValidationIssue{Source: issue.Source, Field: issue.Field, Message: issue.Message, Suggestion: issue.Suggestion, Severity: issue.Severity}
	}
	return preserveSeverity(doc.Attribute(issues))
}
