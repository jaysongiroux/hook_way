package flowconfig

import (
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// ExpandConfigEnv preserves literal JSON Schema keys and references while using
// the existing raw-text expander everywhere else. Source edits touch only dollar
// signs, so YAML quoting, comments and exact numeric literals stay unchanged.
func ExpandConfigEnv(raw string, expand func(string) (string, []string)) (string, []string) {
	var document yaml.Node
	if yaml.Unmarshal([]byte(raw), &document) != nil || len(document.Content) == 0 {
		// Preserve existing expansion/error ordering for malformed YAML.
		return expand(raw)
	}
	lines := []int{0}
	for i, c := range raw {
		if c == '\n' {
			lines = append(lines, i+1)
		}
	}
	positions := map[int]bool{}
	visited := map[*yaml.Node]bool{}
	var protectSchema func(*yaml.Node)
	protectScalar := func(node *yaml.Node) {
		if node.Kind == yaml.AliasNode {
			node = node.Alias
		}
		if node == nil || node.Kind != yaml.ScalarNode || node.Line < 1 || node.Line > len(lines) {
			return
		}
		start := lines[node.Line-1]
		// yaml.Node columns count Unicode characters, not UTF-8 bytes.
		for column := 1; column < node.Column && start < len(raw); column++ {
			_, size := utf8.DecodeRuneInString(raw[start:])
			start += size
		}
		end := schemaScalarEnd(raw, start)
		for i := start; i < end; i++ {
			if raw[i] != '$' {
				continue
			}
			if i+1 < end && raw[i+1] == '$' {
				i++
				continue
			} // Keep existing $$ escape semantics.
			positions[i] = true
		}
	}
	protectSchema = func(node *yaml.Node) {
		if node == nil || visited[node] {
			return
		}
		visited[node] = true
		if node.Kind == yaml.AliasNode {
			protectSchema(node.Alias)
			return
		}
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				protectScalar(key)
				switch key.Value {
				case "$ref", "$dynamicRef", "$recursiveRef", "$$ref", "$$dynamicRef", "$$recursiveRef":
					protectScalar(value)
				}
				protectSchema(value)
			}
		} else {
			for _, child := range node.Content {
				protectSchema(child)
			}
		}
	}
	walked := map[*yaml.Node]bool{}
	var walkNodes func(*yaml.Node)
	walkNodes = func(node *yaml.Node) {
		if node == nil || walked[node] {
			return
		}
		walked[node] = true
		if node.Kind == yaml.AliasNode {
			walkNodes(node.Alias)
			return
		}
		if node.Kind == yaml.MappingNode {
			fields := map[string]*yaml.Node{}
			for i := 0; i+1 < len(node.Content); i += 2 {
				fields[node.Content[i].Value] = node.Content[i+1]
			}
			typ := fields["type"]
			if typ != nil && typ.Value == "mcp_trigger" {
				protectSchema(fields["tools"])
			}
			if typ != nil && typ.Value == "json_validate" {
				protectSchema(fields["schema"])
			}
			if typ != nil && typ.Value == "transform" && fields["kind"] != nil && fields["kind"].Value == "json_schema" {
				protectSchema(fields["json_schema"])
			}
			for key, value := range fields {
				if (typ != nil && typ.Value == "json_validate" && key == "schema") || (typ != nil && typ.Value == "transform" && key == "json_schema") {
					continue
				}
				walkNodes(value)
			}
		} else {
			for _, child := range node.Content {
				walkNodes(child)
			}
		}
	}
	root := document.Content[0]
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "events" {
				protectSchema(root.Content[i+1])
			}
			if root.Content[i].Value == "flows" {
				walkNodes(root.Content[i+1])
			}
		}
	}
	if len(positions) == 0 {
		return expand(raw)
	}
	// NUL cannot occur in YAML source or environment values, so this sentinel
	// cannot collide with literal configuration text or expansion output.
	sentinel := "\x00hookway-schema-dollar\x00"
	for strings.Contains(raw, sentinel) {
		sentinel += "\x00"
	}
	indexes := make([]int, 0, len(positions))
	for index := range positions {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	var masked strings.Builder
	cursor := 0
	for _, index := range indexes {
		masked.WriteString(raw[cursor:index])
		masked.WriteString(sentinel)
		cursor = index + 1
	}
	masked.WriteString(raw[cursor:])
	expanded, missing := expand(masked.String())
	return strings.ReplaceAll(expanded, sentinel, "$"), missing
}

// schemaScalarEnd recognizes only the YAML scalar styles whose bytes can contain
// dollars. The YAML parser has already established their syntax and position.
func schemaScalarEnd(raw string, start int) int {
	if start >= len(raw) {
		return start
	}
	switch raw[start] {
	case '\'', '"':
		quote := raw[start]
		for i := start + 1; i < len(raw); i++ {
			if quote == '"' && raw[i] == '\\' {
				i++
				continue
			}
			if raw[i] != quote {
				continue
			}
			if quote == '\'' && i+1 < len(raw) && raw[i+1] == '\'' {
				i++
				continue
			}
			return i + 1
		}
		return len(raw)
	case '|', '>':
		end := strings.IndexByte(raw[start:], '\n')
		if end < 0 {
			return len(raw)
		}
		end += start + 1
		headerStart := strings.LastIndexByte(raw[:start], '\n') + 1
		headerLine := raw[headerStart:start]
		headerIndent := len(headerLine) - len(strings.TrimLeft(headerLine, " "))
		// Block content extends through blank lines and lines indented beyond its
		// header. Explicit indentation indicators are already checked by YAML.
		for end < len(raw) {
			lineEnd := strings.IndexByte(raw[end:], '\n')
			if lineEnd < 0 {
				lineEnd = len(raw)
			} else {
				lineEnd += end + 1
			}
			line := strings.TrimSuffix(raw[end:lineEnd], "\n")
			spaces := len(line) - len(strings.TrimLeft(line, " "))
			if strings.TrimSpace(line) != "" && spaces <= headerIndent {
				break
			}
			end = lineEnd
		}
		return end
	default:
		end := start
		for end < len(raw) {
			c := raw[end]
			if c == '\n' || c == '\r' || c == ',' || c == '}' || c == ']' || (c == ':' && (end+1 == len(raw) || strings.ContainsRune(" \t\r\n,{}[]", rune(raw[end+1])))) || (c == '#' && (end == start || raw[end-1] == ' ' || raw[end-1] == '\t')) {
				break
			}
			end++
		}
		return end
	}
}
