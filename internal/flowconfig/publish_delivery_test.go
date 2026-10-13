package flowconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
)

func TestPublishCanonicalizationCancellationRemainsRetryable(t *testing.T) {
	raw := []byte(`{"sample":true}`)
	representation, err := jsoncanon.Prepare(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, cached := range []*jsoncanon.Representation{nil, representation} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		env := graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Body: raw}, OriginalJSON: cached}}
		_, err := prepareDelivery(ctx, PublishOptions{WireBody: "json_js"}, "events", env)
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != 503 || ne.Reason == "body_error" || !errors.Is(err, context.Canceled) {
			t.Fatalf("valid body cancellation misclassified: %v (%+v)", err, ne)
		}
	}
}

func TestPublishFrozenBytesAndHashIDs(t *testing.T) {
	raw := []byte(` { "b": 2, "a": 1 } `)
	env := graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Body: raw}}, Body: map[string]any{"changed": true}, BodyFormat: "json"}
	identity := PublishIdentityOptions{Source: "json_js_sha256", HashLength: 10}
	opts := PublishOptions{Destination: "broker", Target: "events.fifo", WireBody: "json_js", FIFO: &PublishFIFOOptions{GroupID: identity, DeduplicationID: &identity}, Retry: &PublishRetryOptions{MaxAttempts: 3, InitialBackoff: "100ms", MaxBackoff: "200ms"}}
	_, prepared, err := PreparePublishForNode("", opts, env)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"b":2,"a":1}`
	sum := sha256.Sum256([]byte(body))
	id := hex.EncodeToString(sum[:])[:10]
	if string(prepared.Publish.Body) != body || prepared.Publish.FIFO.GroupID != id || prepared.Publish.FIFO.DeduplicationID != id {
		t.Fatalf("prepared = %#v", prepared.Publish)
	}
	if env.Publish != nil || !reflect.DeepEqual(env.Body, map[string]any{"changed": true}) {
		t.Fatal("preparation mutated branch input")
	}
	prepared.V.Raw.Body = []byte(`{"different":true}`)
	_, resumed, err := PreparePublishForNode("", opts, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resumed.Publish, prepared.Publish) || resumed.Publish == prepared.Publish {
		t.Fatal("resume changed frozen metadata or shared pointer")
	}
	opts.Target = "other.fifo"
	opts.WireBody = "json"
	_, other, err := PreparePublishForNode("", opts, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if string(other.Publish.Body) != `{"changed":true}` || other.Publish == prepared.Publish {
		t.Fatal("different target inherited frozen bytes")
	}
}

func TestPublishIdentitySelection(t *testing.T) {
	env := graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Body: []byte("raw"), Head: graph.RequestHead{Header: http.Header{"X-Id": {"header-id"}}}}}, Body: map[string]any{"nested": map[string]any{"id": "body-id"}}, Vars: map[string]string{"id": "var-id"}}
	sum := sha256.Sum256([]byte("raw"))
	cases := []struct {
		o    PublishIdentityOptions
		want string
	}{
		{PublishIdentityOptions{Source: "static", Value: "{id}"}, "var-id"},
		{PublishIdentityOptions{Source: "body", Path: "nested.id"}, "body-id"},
		{PublishIdentityOptions{Source: "header", Path: "X-Id"}, "header-id"},
		{PublishIdentityOptions{Source: "variable", Path: "id"}, "var-id"},
		{PublishIdentityOptions{Source: "body", Path: "missing", Fallback: "{id}"}, "var-id"},
		{PublishIdentityOptions{Source: "raw_sha256"}, hex.EncodeToString(sum[:])},
		{PublishIdentityOptions{Source: "body", Path: "missing", FallbackHash: "raw_sha256", HashLength: 10}, hex.EncodeToString(sum[:])[:10]},
	}
	for _, c := range cases {
		got, err := resolvePublishIdentity(c.o, env, nil)
		if err != nil || got != c.want {
			t.Errorf("%+v = %q %v; want %q", c.o, got, err, c.want)
		}
	}
	for _, id := range []string{"", "has space", "é", strings.Repeat("x", 129)} {
		_, err := resolvePublishIdentity(PublishIdentityOptions{Source: "variable", Path: "bad"}, graph.Envelope{Vars: map[string]string{"bad": id}}, nil)
		if err == nil {
			t.Errorf("accepted invalid ID %q", id)
		}
	}
}

func TestPublishDeliveryValidation(t *testing.T) {
	id := PublishIdentityOptions{Source: "static", Value: "group"}
	for _, opts := range []PublishOptions{
		{FIFO: &PublishFIFOOptions{GroupID: PublishIdentityOptions{Source: "static", Value: "invalid space"}, ContentBased: true}},
		{FIFO: &PublishFIFOOptions{GroupID: PublishIdentityOptions{Source: "body", Path: "id", FallbackHash: "raw_sha256", Fallback: "two-fallbacks"}, ContentBased: true}},
		{WireBody: "unknown"}, {Payload: "object", WireBody: "raw"},
		{FIFO: &PublishFIFOOptions{GroupID: id}},
		{FIFO: &PublishFIFOOptions{GroupID: id, DeduplicationID: &id, ContentBased: true}},
		{FIFO: &PublishFIFOOptions{GroupID: PublishIdentityOptions{Source: "body"}, ContentBased: true}},
		{Retry: &PublishRetryOptions{MaxAttempts: 0}}, {Retry: &PublishRetryOptions{MaxAttempts: 3, InitialBackoff: "2s", MaxBackoff: "1s"}},
	} {
		if err := validatePublishDelivery(opts); err == nil {
			t.Errorf("accepted invalid options %+v", opts)
		}
	}
}

func TestPublishNodesPrepareTheirOwnFIFOIdentity(t *testing.T) {
	input := graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"x":1}`)}}, Body: map[string]any{"x": 1.0}, BodyFormat: "json"}
	resolver := &preparingResolver{}
	for _, group := range []string{"first", "second"} {
		opts := PublishOptions{Destination: "broker", Target: "events.fifo", WireBody: "json", FIFO: &PublishFIFOOptions{GroupID: PublishIdentityOptions{Source: "static", Value: group}, ContentBased: true}}
		node, err := newPublishNode(group, opts, resolver)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = node.Run(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if input.Publish != nil || len(resolver.calls) != 2 || resolver.calls[0].env.Publish.FIFO.GroupID != "first" || resolver.calls[1].env.Publish.FIFO.GroupID != "second" {
		t.Fatal("publish branches shared delivery metadata")
	}
	resolver.calls[0].env.Publish.Body[0] = 'x'
	if string(resolver.calls[1].env.Publish.Body) != `{"x":1}` {
		t.Fatal("publish branches shared rendered bytes")
	}
}

func TestResumeDoesNotFreezeSameTargetSuccessContinuation(t *testing.T) {
	input := graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Body: []byte(`{"x":1}`)}}}
	opts := PublishOptions{Destination: "broker", Target: "events.fifo", WireBody: "raw", FIFO: &PublishFIFOOptions{GroupID: PublishIdentityOptions{Source: "static", Value: "first"}, ContentBased: true}}
	_, snapshotInput, err := PreparePublishForNode("first", opts, input)
	if err != nil {
		t.Fatal(err)
	}
	opts.FIFO = &PublishFIFOOptions{GroupID: PublishIdentityOptions{Source: "static", Value: "second"}, ContentBased: true}
	_, continuation, err := PreparePublishForNode("second", opts, snapshotInput)
	if err != nil {
		t.Fatal(err)
	}
	if continuation.Publish.FIFO.GroupID != "second" || continuation.Publish.NodeID != "second" || snapshotInput.Publish.FIFO.GroupID != "first" {
		t.Fatal("continuation inherited another node's frozen identity")
	}
}
