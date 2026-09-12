package queue

import (
	"github.com/jasongiroux/hookway/internal/vars"
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
	if value := resolveSourceValue(msg, s.GroupIDSource, s.GroupIDValue, s.GroupIDPath); value != "" {
		return value
	}
	return fallbackGroupID(msg, s.GroupIDFallback)
}

// resolveSourceValue extracts the raw value for a static | endpoint | header |
// body source without applying any fallback; callers decide what an empty
// result means. Shared by FIFO group ids, Kafka partition keys, and Temporal
// workflow ids.
func resolveSourceValue(msg Message, source, value, path string) string {
	switch source {
	case "static":
		return value
	case "endpoint":
		return msg.Endpoint
	case "header":
		if msg.Headers != nil {
			return msg.Headers.Get(path)
		}
	case "body":
		v, _ := vars.BodyString(msg.Body, path)
		return v
	}
	return ""
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
