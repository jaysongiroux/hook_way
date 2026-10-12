package flowconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/jaysongiroux/hook_way/internal/config"
	"gopkg.in/yaml.v3"
)

// ResolvedDocument carries assembled YAML and the ownership of logical flow paths.
// Resolving files never expands environment variables; expansion happens afterward.
type ResolvedDocument struct {
	Data        []byte
	Source      string
	FlowSources map[string]string
}

// YAMLPath validates a portable, literal path within the entry directory.
func YAMLPath(name string) (string, error) {
	clean := path.Clean(name)
	if name == "" || strings.ContainsAny(name, `\:$*?[]`) || strings.IndexFunc(name, unicode.IsControl) >= 0 || path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || (path.Ext(clean) != ".yaml" && path.Ext(clean) != ".yml") {
		return "", fmt.Errorf("must be a literal relative .yaml or .yml path inside the entry directory")
	}
	return clean, nil
}

// Attribute keeps logical fields stable while identifying their deployment file.
func (d ResolvedDocument) Attribute(issues []config.ValidationIssue) []config.ValidationIssue {
	for i := range issues {
		issue := &issues[i]
		if issue.Source != "" {
			continue
		}
		issue.Source = d.Source
		longest := 0
		for name, source := range d.FlowSources {
			prefix := "flows." + name
			if (issue.Field == prefix || strings.HasPrefix(issue.Field, prefix+".") || strings.HasPrefix(issue.Field, prefix+"[")) && len(prefix) > longest {
				issue.Source = source
				longest = len(prefix)
			}
		}
	}
	return issues
}

func yamlMapping(data []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var trailing yaml.Node
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("multiple YAML documents are not supported")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("must be a YAML mapping")
	}
	// Reject duplicates before replacing flow references, including nested keys.
	var check map[string]any
	if err := doc.Decode(&check); err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// Resolve assembles named flow files using only the supplied reader. A nil reader
// deliberately makes byte-only validation independent of the host filesystem.
func Resolve(data []byte, source string, read func(string) ([]byte, error)) (ResolvedDocument, []config.ValidationIssue) {
	out := ResolvedDocument{Data: data, Source: source, FlowSources: map[string]string{}}
	root, err := yamlMapping(data)
	if err != nil {
		return out, out.Attribute([]config.ValidationIssue{cfgIssue("config", err.Error(), "")})
	}
	flows := mappingValue(root, "flows")
	if flows == nil || flows.Kind != yaml.MappingNode {
		return out, nil
	}
	var issues []config.ValidationIssue
	inline, referenced := false, false
	seen := map[string]bool{}
	for i := 0; i < len(flows.Content); i += 2 {
		name, flow := flows.Content[i].Value, flows.Content[i+1]
		ref := mappingValue(flow, "file")
		if ref == nil {
			inline = true
			continue
		}
		referenced = true
		field := "flows." + name + ".file"
		fail := func(message string) { issues = append(issues, cfgIssue(field, message, "")) }
		if len(flow.Content) != 2 {
			fail("a file reference cannot contain inline flow fields")
			continue
		}
		if ref.Kind != yaml.ScalarNode || ref.Tag != "!!str" {
			fail("must be a YAML filename string")
			continue
		}
		filename, err := YAMLPath(ref.Value)
		if err != nil {
			fail(err.Error())
			continue
		}
		if seen[filename] {
			fail("the same file cannot be referenced by multiple flows")
			continue
		}
		seen[filename] = true
		if read == nil {
			fail("flow file is not supplied: " + filename)
			continue
		}
		content, err := read(filename)
		if err != nil {
			fail(fmt.Sprintf("read %s: %v", filename, err))
			continue
		}
		body, err := yamlMapping(content)
		fileSource := path.Join(path.Dir(filepath.ToSlash(source)), filename)
		if err != nil {
			issue := cfgIssue("flows."+name, err.Error(), "")
			issue.Source = fileSource
			issues = append(issues, issue)
			continue
		}
		// Central sections, wrappers and recursive references are structural errors.
		for j := 0; j < len(body.Content); j += 2 {
			switch body.Content[j].Value {
			case "nodes", "edges", "responses", "delivery_failure_policy", "capture_failures":
			default:
				issue := cfgIssue("flows."+name+"."+body.Content[j].Value, "flow files contain only flow fields; central settings and nested includes belong in the entry file", "")
				issue.Source = fileSource
				issues = append(issues, issue)
			}
		}
		flows.Content[i+1] = body
		out.FlowSources[name] = fileSource
	}
	if inline && referenced {
		issues = append(issues, cfgIssue("flows", "inline flows and file references cannot be mixed", "use either all inline flows or one file per flow"))
	}
	if len(issues) != 0 {
		return out, out.Attribute(issues)
	}
	if referenced {
		out.Data, err = yaml.Marshal(root)
		if err != nil {
			return out, out.Attribute([]config.ValidationIssue{cfgIssue("config", err.Error(), "")})
		}
	}
	return out, nil
}

// ResolveFile uses os.Root to enforce directory confinement even if a symlink
// changes while a flow file is being opened. It also rejects physical aliases.
func ResolveFile(filename string) (ResolvedDocument, []config.ValidationIssue) {
	data, err := os.ReadFile(filepath.Clean(filename))
	if err != nil {
		return ResolvedDocument{}, []config.ValidationIssue{{Field: "config", Message: fmt.Sprintf("read %s: %v", filename, err), Source: filename}}
	}
	root, err := os.OpenRoot(filepath.Dir(filename))
	if err != nil {
		return ResolvedDocument{}, []config.ValidationIssue{{Field: "config", Message: err.Error(), Source: filename}}
	}
	defer func() { _ = root.Close() }() // Read-only directory handle.
	var opened []os.FileInfo
	return Resolve(data, filename, func(name string) ([]byte, error) {
		file, err := root.Open(filepath.FromSlash(name))
		if err != nil {
			return nil, err
		}
		defer func() { _ = file.Close() }() // Read-only file; read errors are returned below.
		info, err := file.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("flow file must be a regular file")
		}
		for _, previous := range opened {
			if os.SameFile(info, previous) {
				return nil, errors.New("the same file cannot be referenced by multiple flows")
			}
		}
		opened = append(opened, info)
		return io.ReadAll(file)
	})
}

// LoadFile is the disk entry point for both expanding and --no-env CLI modes.
func LoadFile(filename string, expandEnv bool) (Config, []config.ValidationIssue) {
	doc, issues := ResolveFile(filename)
	if len(issues) != 0 {
		// A legacy inline config may not be parseable until env expansion. Falling
		// back to the byte-only loader preserves that behavior without permitting
		// environment-derived include paths or reading any referenced file.
		if expandEnv && len(issues) == 1 && issues[0].Field == "config" {
			if raw, err := os.ReadFile(filepath.Clean(filename)); err == nil && bytes.Contains(raw, []byte("$")) {
				cfg, legacyIssues := LoadBytes(raw, filename, true)
				return cfg, (ResolvedDocument{Source: filename}).Attribute(legacyIssues)
			}
		}
		return Config{}, issues
	}
	cfg, issues := loadInlineBytes(doc.Data, filename, expandEnv)
	return cfg, doc.Attribute(issues)
}
