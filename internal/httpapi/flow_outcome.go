package httpapi

import (
	"errors"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func flowOutcome(mf mountedFlow, term graph.Terminal, err error, dropped, publishFailed bool) string {
	published, failed := deliveryMaps(mf, term)
	if len(published) > 0 && (len(failed) > 0 || publishFailed || err != nil) {
		return "partial_failure"
	}
	var specific graph.NodeError
	if errors.As(err, &specific) && specific.Reason == "auth_unavailable" {
		return "error"
	}
	if errors.As(err, &specific) && specific.Reason == "body_error" {
		return "body_error"
	}
	attemptedFailure := false
	for _, delivery := range term.Deliveries {
		if delivery.Err != nil && !delivery.Unattempted {
			attemptedFailure = true
		}
	}
	if attemptedFailure || publishFailed {
		return "publish_error"
	}
	if err != nil {
		if statusFromNodeError(err) == http.StatusRequestEntityTooLarge {
			return "payload_too_large"
		}
		var ne graph.NodeError
		if errors.As(err, &ne) {
			switch mf.nodeTypes[ne.NodeID] {
			case "auth":
				return "auth_error"
			case "read_body", "envelope":
				return "body_error"
			case "lookup", "route":
				return "lookup_error"
			}
		}
		return "error"
	}
	if len(published) > 0 {
		return "published"
	}
	if dropped {
		return "dropped"
	}
	if term.Response != nil {
		return "responded"
	}
	return "error"
}
