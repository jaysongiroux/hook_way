package config

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// safeConnectionName is deliberately stricter than IsSafeName, which also
// permits ".". Issue fields are dotted paths resolved segment by segment (see
// configapi.lookupFieldPath), so a dot inside a name makes the path ambiguous.
// Destination ids already carry that wart; a new namespace should not inherit
// it.
var safeConnectionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// connectionBindingIssues validates the connections block and every reference
// to it, working on the parsed YAML node tree rather than the decoded structs.
//
// The node tree is not a convenience here, it is a requirement. Go zero values
// cannot distinguish "absent" from "explicitly set to the zero value", so a
// struct-based check for "this destination sets a field its connection already
// owns" would silently miss `redis.db: 0`, `postgres.max_conns: 0`, and
// `tls.insecure_skip_verify: false`. The node tree knows a key was written.
//
// It also gives exact field paths for free, which matters beyond cosmetics:
// configapi.ValidateWithEnv downgrades a structural error to a warning by
// looking the issue's field path up in the YAML, so a path naming something
// the operator never wrote would break validation-without-secrets in CI.
func connectionBindingIssues(root *yaml.Node) []ValidationIssue {
	var issues []ValidationIssue
	conns := mappingValue(root, "connections")
	if conns == nil && mappingValue(root, "destinations") == nil {
		return nil
	}

	connTypes := make(map[string]string)
	for _, name := range mappingKeys(conns) {
		entry := mappingValue(conns, name)
		field := joinYAMLPath("connections", name)
		if !safeConnectionName.MatchString(name) {
			issues = append(issues, validationError(field,
				"connection name contains unsupported characters",
				"use letters, numbers, underscore, or hyphen only"))
		}
		connType := scalarValue(entry, "type")
		switch {
		case connType == "":
			issues = append(issues, validationError(joinYAMLPath(field, "type"),
				"connection type is required",
				"set type to one of "+quotedList(connectionTypes)))
			continue
		case connectionShapes[connType] == nil:
			issues = append(issues, validationError(joinYAMLPath(field, "type"),
				fmt.Sprintf("%q is not supported", connType),
				"supported values are "+quotedList(connectionTypes)))
			continue
		}
		connTypes[name] = connType
		issues = append(issues, unownedConnectionKeyIssues(entry, field, connType)...)
	}

	referenced := make(map[string]bool, len(connTypes))
	issues = append(issues, destinationConnectionIssues(root, connTypes, referenced)...)
	issues = append(issues, replayConnectionIssues(root, connTypes, referenced)...)

	for _, name := range mappingKeys(conns) {
		if _, known := connTypes[name]; !known || referenced[name] {
			continue
		}
		issues = append(issues, ValidationIssue{
			Field:      joinYAMLPath("connections", name),
			Message:    "connection is not referenced by any destination or by replay",
			Suggestion: "reference it from a destination's connection: field, or remove it",
			Severity:   SeverityWarning,
		})
	}
	return issues
}

// unownedConnectionKeyIssues reports keys that are known connection fields but
// are not legal for this connection's type -- brokers on an aws connection, or
// tls.enabled on a postgres one. unknownYAMLFields cannot catch these: the
// decode target is the union of every type's fields, because a connection is
// authored flat.
func unownedConnectionKeyIssues(entry *yaml.Node, field, connType string) []ValidationIssue {
	owned := ownedConnPaths(connType)
	known := knownConnectionPaths()
	var issues []ValidationIssue
	for _, path := range writtenPaths(entry, "") {
		if path == "type" {
			continue
		}
		if _, ok := owned[path]; ok {
			continue
		}
		// A key that is not a connection field at all -- a typo -- is already
		// reported by unknownYAMLFields against rawConnectionConfig. Saying
		// "and it is not valid for type aws" on top of that is noise.
		if _, ok := known[path]; !ok {
			continue
		}
		issues = append(issues, validationError(joinYAMLPath(field, path),
			fmt.Sprintf("field %s is not valid for a connection of type %q", path, connType),
			fmt.Sprintf("remove %s, or change the connection type", path)))
	}
	return issues
}

func destinationConnectionIssues(root *yaml.Node, connTypes map[string]string, referenced map[string]bool) []ValidationIssue {
	dests := mappingValue(root, "destinations")
	var issues []ValidationIssue
	for _, id := range mappingKeys(dests) {
		entry := mappingValue(dests, id)
		name := scalarValue(entry, "connection")
		if name == "" {
			continue
		}
		field := joinYAMLPath(joinYAMLPath("destinations", id), "connection")
		destType := scalarValue(entry, "type")
		connType, ok := connTypes[name]
		if !ok {
			issues = append(issues, validationError(field,
				fmt.Sprintf("references unknown connection %q", name),
				knownConnectionsSuggestion(connTypes)))
			continue
		}
		referenced[name] = true
		want, known := connTypeForDestType[destType]
		if !known {
			// The destination type is missing or unsupported; validationIssues
			// already reports that, and naming a second problem here would
			// just be noise.
			continue
		}
		if connType != want {
			issues = append(issues, validationError(field,
				fmt.Sprintf("connection %q has type %q, which cannot serve a %q destination", name, connType, destType),
				fmt.Sprintf("use a connection of type %q, or remove connection: and configure the destination inline", want)))
			continue
		}
		issues = append(issues, overriddenFieldIssues(entry, joinYAMLPath("destinations", id), destTypeBlocks[destType], connType, name)...)
	}
	return issues
}

func replayConnectionIssues(root *yaml.Node, connTypes map[string]string, referenced map[string]bool) []ValidationIssue {
	replay := mappingValue(root, "replay")
	name := scalarValue(replay, "connection")
	if name == "" {
		return nil
	}
	connType, ok := connTypes[name]
	if !ok {
		return []ValidationIssue{validationError("replay.connection",
			fmt.Sprintf("references unknown connection %q", name),
			knownConnectionsSuggestion(connTypes))}
	}
	referenced[name] = true
	if connType != "postgres" {
		return []ValidationIssue{validationError("replay.connection",
			fmt.Sprintf("connection %q has type %q, but the replay store requires a %q connection", name, connType, "postgres"),
			"use a connection of type \"postgres\", or configure replay.postgres inline")}
	}
	return overriddenFieldIssues(replay, "replay", "postgres", connType, name)
}

// overriddenFieldIssues reports connection-owned fields written inside the
// owner's backend block. The strict split means a field belongs to exactly one
// of the two places.
func overriddenFieldIssues(entry *yaml.Node, ownerField, block, connType, connName string) []ValidationIssue {
	blockNode := mappingValue(entry, block)
	if blockNode == nil {
		return nil
	}
	owned := ownedConnPaths(connType)
	var issues []ValidationIssue
	for _, path := range writtenPaths(blockNode, "") {
		if _, ok := owned[path]; !ok {
			continue
		}
		full := joinYAMLPath(joinYAMLPath(ownerField, block), path)
		issues = append(issues, validationError(full,
			fmt.Sprintf("%s is provided by connection %q and must not be set on the destination", path, connName),
			fmt.Sprintf("remove %s, or remove connection: %s and configure the destination inline", path, connName)))
	}
	return issues
}

func knownConnectionsSuggestion(connTypes map[string]string) string {
	if len(connTypes) == 0 {
		return "define it under connections, or remove connection: and configure the destination inline"
	}
	return "known connections are " + quotedList(sortedStringKeys(connTypes))
}

// writtenPaths returns the dotted paths actually present in a mapping node,
// recursing into nested mappings. A nested mapping yields both its own path
// and its children's, so "tls" and "tls.ca_cert" are both reported and the
// caller can match whichever granularity it owns.
func writtenPaths(node *yaml.Node, prefix string) []string {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		path := joinYAMLPath(prefix, key.Value)
		if value.Kind == yaml.MappingNode {
			children := writtenPaths(value, path)
			if len(children) > 0 {
				out = append(out, children...)
				continue
			}
		}
		out = append(out, path)
	}
	return out
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// mappingKeys returns a mapping node's keys in document order, so issues are
// emitted deterministically without needing a sort.
func mappingKeys(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]string, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		out = append(out, node.Content[i].Value)
	}
	return out
}

// scalarValue returns the scalar string at key, or "" when absent or not a
// scalar.
func scalarValue(node *yaml.Node, key string) string {
	v := mappingValue(node, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}
