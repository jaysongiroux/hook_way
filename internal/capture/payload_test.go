package capture

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

func TestRenderPayloadIsTheEnvelopeForEveryDestinationType(t *testing.T) {
	msg := richMessage()
	msg.PayloadMode = "envelope"
	want, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	types := config.DestinationTypeSchemas()
	if len(types) == 0 {
		t.Fatal("no destination types registered")
	}
	for typ := range types {
		t.Run(typ, func(t *testing.T) {
			p, err := RenderPayload(config.DestinationConfig{Type: typ}, msg)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(p.Bytes, want) || p.ContentType != "application/json" {
				t.Fatalf("%s payload = %s (%s), want the JSON envelope", typ, p.Bytes, p.ContentType)
			}
		})
	}
}

func TestRenderPayloadObjectMode(t *testing.T) {
	msg := richMessage()
	for _, typ := range []string{"s3", "redis_kv"} {
		p, err := RenderPayload(config.DestinationConfig{Type: typ}, msg)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(p.Bytes, msg.ObjectBytes) || p.ContentType != msg.ObjectContentType || p.Filename != msg.ObjectFilename {
			t.Fatalf("%s object payload = %+v", typ, p)
		}
	}
}

func TestRenderPayloadProxyWebhookIsTheOriginalBody(t *testing.T) {
	msg := richMessage()
	msg.OrigHeaders = http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	dest := config.DestinationConfig{Type: "webhook", Webhook: config.WebhookConfig{Auth: config.WebhookAuthConfig{Type: "proxy"}}}
	p, err := RenderPayload(dest, msg)
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Bytes) != "raw" || p.ContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("proxy payload = %q (%s)", p.Bytes, p.ContentType)
	}
}

func TestRenderInputBody(t *testing.T) {
	doc, err := transform.ParseXML([]byte("<a><b>1</b></a>"))
	if err != nil {
		t.Fatal(err)
	}
	xmlBytes, err := transform.SerializeXML(doc)
	if err != nil {
		t.Fatal(err)
	}
	raw := graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{
		Head: graph.RequestHead{Header: http.Header{"Content-Type": {"text/plain"}}},
		Body: []byte("plain"),
	}}}
	cases := map[string]struct {
		env         graph.Envelope
		body, ctype string
	}{
		"raw":  {raw, "plain", "text/plain"},
		"xml":  {graph.Envelope{Body: doc}, string(xmlBytes), "application/xml"},
		"json": {graph.Envelope{Body: map[string]any{"a": 1.0}}, `{"a":1}`, "application/json"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := RenderInputBody(c.env)
			if err != nil {
				t.Fatal(err)
			}
			if string(p.Bytes) != c.body || p.ContentType != c.ctype {
				t.Fatalf("RenderInputBody = %q (%s), want %q (%s)", p.Bytes, p.ContentType, c.body, c.ctype)
			}
		})
	}
}
