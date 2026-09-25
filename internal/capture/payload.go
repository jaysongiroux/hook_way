package capture

import (
	"encoding/json"
	"fmt"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

// Payload is the downloadable body of a failure record: what the destination
// would have received, or a non-publish node's input body.
type Payload struct {
	Bytes       []byte
	ContentType string
	Filename    string
}

// RenderPayload returns what dest would receive for msg: the object bytes for
// s3 and redis_kv in object mode, the original request body for a proxy-mode
// webhook, and the JSON envelope — byte-for-byte what the adapters send — for
// everything else. Flows cannot select Temporal's body input, so Temporal
// always receives the envelope as well.
func RenderPayload(dest config.DestinationConfig, msg queue.Message) (Payload, error) {
	switch {
	case (dest.Type == "s3" || dest.Type == "redis_kv") && msg.PayloadMode == "object":
		return Payload{Bytes: msg.ObjectBytes, ContentType: msg.ObjectContentType, Filename: msg.ObjectFilename}, nil
	case dest.Type == "webhook" && dest.Webhook.Auth.Type == "proxy":
		return Payload{Bytes: msg.RawBody, ContentType: msg.OrigHeaders.Get("Content-Type")}, nil
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return Payload{}, fmt.Errorf("render payload: %w", err)
	}
	return Payload{Bytes: b, ContentType: "application/json"}, nil
}

// RenderInputBody returns a non-publish node's input body in wire form: an
// XML document serialized, a parsed body as JSON, and the raw request bytes
// when the envelope carries no parsed body.
func RenderInputBody(env graph.Envelope) (Payload, error) {
	switch body := env.Body.(type) {
	case nil:
		return Payload{Bytes: env.V.Raw.Body, ContentType: env.V.Raw.Head.Header.Get("Content-Type")}, nil
	case transform.Document:
		return renderXML(body)
	case *transform.Document:
		if body == nil {
			return Payload{}, fmt.Errorf("%w: nil *transform.Document", ErrUnsupported)
		}
		return renderXML(*body)
	default:
		b, err := json.Marshal(body)
		if err != nil {
			return Payload{}, fmt.Errorf("render input body: %w", err)
		}
		return Payload{Bytes: b, ContentType: "application/json"}, nil
	}
}

func renderXML(doc transform.Document) (Payload, error) {
	b, err := transform.SerializeXML(doc)
	if err != nil {
		return Payload{}, fmt.Errorf("render input body: %w", err)
	}
	return Payload{Bytes: b, ContentType: "application/xml"}, nil
}
