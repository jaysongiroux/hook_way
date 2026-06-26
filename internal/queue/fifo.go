package queue

import (
	"encoding/json"
	"strconv"
	"strings"
)

// FIFOSettings is the queue-package view of a destination's per-target FIFO
// configuration, populated by the factory from config.FIFOConfig.
type FIFOSettings struct {
	GroupIDSource   string // static | endpoint | header | body
	GroupIDValue    string // used when GroupIDSource == "static"
	GroupIDPath     string // used when GroupIDSource == "header" | "body"
	GroupIDFallback string // literal value, or "endpoint"

	DedupUseIdempotencyKey    bool
	DedupContentBasedFallback bool
}

// resolveFIFO derives the MessageGroupId and MessageDeduplicationId for a FIFO
// publish. contentBasedDedup reports whether the publisher should rely on the
// resource's content-based deduplication instead of sending an explicit id.
func resolveFIFO(msg Message, s FIFOSettings) (groupID, dedupID string, contentBasedDedup bool) {
	groupID = resolveGroupID(msg, s)
	if s.DedupUseIdempotencyKey && msg.IdempotencyKey != "" {
		return groupID, msg.IdempotencyKey, false
	}
	if s.DedupContentBasedFallback {
		return groupID, "", true
	}
	return groupID, "", false
}

func resolveGroupID(msg Message, s FIFOSettings) string {
	var value string
	switch s.GroupIDSource {
	case "static":
		value = s.GroupIDValue
	case "endpoint":
		value = msg.Endpoint
	case "header":
		if msg.Headers != nil {
			value = msg.Headers.Get(s.GroupIDPath)
		}
	case "body":
		value = lookupBodyPath(msg.Body, s.GroupIDPath)
	}
	if value != "" {
		return value
	}
	return fallbackGroupID(msg, s.GroupIDFallback)
}

func fallbackGroupID(msg Message, fallback string) string {
	if fallback == "endpoint" {
		return msg.Endpoint
	}
	if fallback != "" {
		return fallback
	}
	return msg.Endpoint
}

// lookupBodyPath walks a dotted path (e.g. "customer.id") into a JSON-decoded
// body and returns the leaf as a string when it is a string; otherwise "".
func lookupBodyPath(body any, path string) string {
	if path == "" {
		return ""
	}
	current := body
	for _, segment := range strings.Split(path, ".") {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = m[segment]
		if !ok {
			return ""
		}
	}
	switch v := current.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}
