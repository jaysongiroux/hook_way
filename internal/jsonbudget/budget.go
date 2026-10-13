// Package jsonbudget checks JSON structure before a decoder allocates its tree.
package jsonbudget

import (
	"encoding/json"
	"errors"
)

const (
	DefaultMaxDepth = 128
	DefaultMaxNodes = 1000000
)

var (
	ErrInvalid = errors.New("invalid JSON body")
	ErrLimit   = errors.New("JSON structure limit exceeded")
)

type Limits struct {
	MaxDepth, MaxNodes int
}

// Check counts semantic values, including values later overwritten by duplicate
// object keys. Keys do not count as separate values. The root has depth zero.
// Byte budgets belong to callers. This scan never builds maps, arrays or strings;
// encoding/json remains responsible for syntax, number and string semantics.
func Check(raw []byte, limits Limits) error {
	if !json.Valid(raw) {
		return ErrInvalid
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = DefaultMaxDepth
	}
	if limits.MaxNodes <= 0 {
		limits.MaxNodes = DefaultMaxNodes
	}
	depth, nodes := 0, 0
	for i := 0; i < len(raw); {
		switch raw[i] {
		case ' ', '\t', '\n', '\r', ':', ',':
			i++
			continue
		case '}', ']':
			depth--
			i++
			continue
		case '{', '[':
			nodes++
			if depth > limits.MaxDepth || nodes > limits.MaxNodes {
				return ErrLimit
			}
			depth++
			i++
			continue
		case '"':
			i++
			for raw[i] != '"' {
				if raw[i] == '\\' {
					i++
				}
				i++
			}
			i++
			for i < len(raw) && space(raw[i]) {
				i++
			}
			if i < len(raw) && raw[i] == ':' {
				continue // object key, not a value
			}
		default:
			for i < len(raw) && raw[i] != ',' && raw[i] != '}' && raw[i] != ']' && !space(raw[i]) {
				i++
			}
		}
		nodes++
		if depth > limits.MaxDepth || nodes > limits.MaxNodes {
			return ErrLimit
		}
	}
	return nil
}

func space(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
