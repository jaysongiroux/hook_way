package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/publishmsg"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

type recordedPublish struct {
	destination, target string
	env                 graph.Envelope
}

// recordingPublisher records every publish and fails the listed destinations
// while failing is set.
type recordingPublisher struct {
	mu      sync.Mutex
	failing map[string]bool
	calls   []recordedPublish
}

func (p *recordingPublisher) Resolve(string) (flowconfig.Publisher, error) { return p, nil }

func (p *recordingPublisher) Publish(_ context.Context, dest, target string, env graph.Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, recordedPublish{dest, target, env})
	if p.failing[dest] {
		return errors.New("destination down")
	}
	return nil
}

func (p *recordingPublisher) lastCall(t *testing.T, dest string) recordedPublish {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.calls) - 1; i >= 0; i-- {
		if p.calls[i].destination == dest {
			return p.calls[i]
		}
	}
	t.Fatalf("no publish recorded for destination %q", dest)
	return recordedPublish{}
}

func (p *recordingPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *recordingPublisher) callsSince(n int) []recordedPublish {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedPublish(nil), p.calls[n:]...)
}

func destinationsOf(calls []recordedPublish) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.destination
	}
	return out
}

type identityCase struct {
	flow        string
	path        string
	pathVars    map[string]string
	contentType string
	body        []byte
	dests       map[string]config.DestinationConfig // destination id → config (only Type/Webhook matter)
	failing     []string
}

func dest(typ string) config.DestinationConfig { return config.DestinationConfig{Type: typ} }

const headNodes = `
  - {id: in, type: http_trigger, path: "/webhooks/{route}", methods: [POST]}
  - {id: body, type: read_body, max_bytes: 4194304}
  - {id: auth, type: no_auth}
`

func identityCases(t *testing.T) map[string]identityCase {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"upstream":true}`))
	}))
	t.Cleanup(upstream.Close)

	var mp bytes.Buffer
	mw := multipart.NewWriter(&mp)
	_ = mw.WriteField("note", "hi")
	fw, _ := mw.CreateFormFile("document", "a.txt")
	_, _ = fw.Write([]byte("hello file"))
	_ = mw.Close()

	// A latin-1 client sends a filename that is not UTF-8; the parser keeps
	// its raw bytes, and S3 derives the object key from them.
	var latin bytes.Buffer
	lw := multipart.NewWriter(&latin)
	lf, _ := lw.CreateFormFile("document", "caf\xe9.txt")
	_, _ = lf.Write([]byte("bonjour"))
	_ = lw.Close()

	allTypes := make([]string, 0, 14)
	for typ := range config.DestinationTypeSchemas() {
		allTypes = append(allTypes, typ)
	}
	sort.Strings(allTypes)
	var fanNodes, fanEdges strings.Builder
	allDests := map[string]config.DestinationConfig{}
	for _, typ := range allTypes {
		fanNodes.WriteString("  - {id: to_" + typ + ", type: publish, destination: " + typ + ", target: events}\n")
		fanEdges.WriteString(", env>to_" + typ)
		allDests[typ] = dest(typ)
	}

	jsonBody := []byte(`{"event":"created","id":"42","count":3,"nested":{"a":[1,2.5,"x",null,true]}}`)
	return map[string]identityCase{
		"json with templated target and attributes": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: json}
  - {id: out, type: publish, destination: queue, target: "orders-{route}", message_attributes: {tenant: "{route}"}}
edges: [in>body, body>auth, auth>env, env>out]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/json", body: jsonBody,
			dests: map[string]config.DestinationConfig{"queue": dest("sqs")}, failing: []string{"queue"},
		},
		"raw body to a proxy webhook": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: raw}
  - {id: out, type: publish, destination: hook, target: inbound}
edges: [in>body, body>auth, auth>env, env>out]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/octet-stream",
			body:    []byte("\x00\x01 not json \xff"),
			dests:   map[string]config.DestinationConfig{"hook": {Type: "webhook", Webhook: config.WebhookConfig{Auth: config.WebhookAuthConfig{Type: "proxy"}}}},
			failing: []string{"hook"},
		},
		"xml pruned to s3": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: xml}
  - {id: prune, type: transform, kind: xml_prune}
  - {id: out, type: publish, destination: archive, target: xml}
edges: [in>body, body>auth, auth>env, env>prune, prune>out]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/xml",
			body:  []byte(`<?xml version="1.0"?><order xmlns:a="urn:a" a:id="7"><item> 1 </item><empty/></order>`),
			dests: map[string]config.DestinationConfig{"archive": dest("s3")}, failing: []string{"archive"},
		},
		"multipart file as an s3 object": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: multipart, multipart: {max_parts: 4, max_part_bytes: 1048576, include_content: true}}
  - {id: pick, type: code, timeout: 1s, script: 'return {body: input.body.files.document}'}
  - {id: out, type: publish, destination: archive, target: uploads, payload: object}
edges: [in>body, body>auth, auth>env, env>pick, pick>out]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: mw.FormDataContentType(), body: mp.Bytes(),
			dests: map[string]config.DestinationConfig{"archive": dest("s3")}, failing: []string{"archive"},
		},
		"multipart with a latin-1 filename": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: multipart, multipart: {max_parts: 4, max_part_bytes: 1048576, include_content: true}}
  - {id: out, type: publish, destination: events, target: uploads}
edges: [in>body, body>auth, auth>env, env>out]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: lw.FormDataContentType(), body: latin.Bytes(),
			dests: map[string]config.DestinationConfig{"events": dest("kafka")}, failing: []string{"events"},
		},
		"code and lookup to redis_kv": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: json}
  - {id: find, type: lookup, provider: dynamodb, key: '{body.id}', name: tenant, table: routing}
  - {id: shape, type: code, timeout: 1s, script: 'return {body: {id: input.body.id, big: 9007199254740991, list: [1, 2, 3], tenant: input.vars.tenant}, vars: input.vars}'}
  - {id: map, type: transform, kind: field_map, mappings: [{from: body.id, to: vars.id}]}
  - {id: out, type: publish, destination: kv, target: "tenant-{tenant}"}
edges: [in>body, body>auth, auth>env, env>find, find>shape, shape>map, map>out]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/json", body: jsonBody,
			dests: map[string]config.DestinationConfig{"kv": dest("redis_kv")}, failing: []string{"kv"},
		},
		"resume mid continuation": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: json}
  - {id: s3, type: publish, destination: archive, target: uploads}
  - {id: shape, type: code, timeout: 1s, script: 'return {body: {archived: true, event: input.body.event}}'}
  - {id: fn, type: publish, destination: workers, target: upload-worker}
edges: [in>body, body>auth, auth>env, env>s3, s3>shape, shape>fn]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/json", body: jsonBody,
			dests:   map[string]config.DestinationConfig{"archive": dest("s3"), "workers": dest("lambda")},
			failing: []string{"workers"},
		},
		"http_request upstream then publish": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: json}
  - {id: call, type: http_request, url: "` + upstream.URL + `"}
  - {id: out, type: publish, destination: docs, target: events}
edges: [in>body, body>auth, auth>env, env>call, call>out]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/json", body: jsonBody,
			dests: map[string]config.DestinationConfig{"docs": dest("mongodb")}, failing: []string{"docs"},
		},
		"switch branch to postgres": {
			flow: "nodes:" + headNodes + `
  - {id: env, type: envelope, body_format: json}
  - {id: sw, type: switch, cases: [{port: created, when: {source: body, path: event, operator: eq, value: created}}], default: other}
  - {id: a, type: publish, destination: rows, target: events}
  - {id: b, type: publish, destination: other_rows, target: events}
edges: [in>body, body>auth, auth>env, env>sw, "sw:created>a", "sw:other>b"]
`,
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/json", body: jsonBody,
			dests: map[string]config.DestinationConfig{"rows": dest("postgres"), "other_rows": dest("postgres")}, failing: []string{"rows"},
		},
		"fan-out to every destination type": {
			flow: "delivery_failure_policy: skip_failed\nnodes:" + headNodes + "  - {id: env, type: envelope, body_format: json}\n" +
				fanNodes.String() + "edges: [in>body, body>auth, auth>env" + fanEdges.String() + "]\n",
			path: "/webhooks/acme", pathVars: map[string]string{"route": "acme"}, contentType: "application/json", body: jsonBody,
			dests: allDests, failing: allTypes,
		},
	}
}

func assertSameMessage(t *testing.T, label string, want, got queue.Message) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("%s: envelope bytes differ:\n want %s\n got  %s", label, wantJSON, gotJSON)
	}
	hidden := func(m queue.Message) []any {
		return []any{m.RawBody, m.OrigHeaders, m.Hops, m.MessageAttributes, m.PayloadMode, m.ObjectBytes, m.ObjectContentType, m.ObjectFilename}
	}
	if !reflect.DeepEqual(hidden(want), hidden(got)) {
		t.Fatalf("%s: in-memory fields differ:\n want %#v\n got  %#v", label, hidden(want), hidden(got))
	}
}

func TestRedriveThroughASnapshotIsByteIdentical(t *testing.T) {
	for name, c := range identityCases(t) {
		t.Run(name, func(t *testing.T) {
			var fc graph.FlowConfig
			if err := yaml.Unmarshal([]byte(c.flow), &fc); err != nil {
				t.Fatal(err)
			}
			failing := map[string]bool{}
			for _, d := range c.failing {
				failing[d] = true
			}
			pub := &recordingPublisher{failing: failing}
			reg, err := flowconfig.NewRegistryWithOptions(flowconfig.RegistryOptions{
				Publishers: pub,
				Logger:     slog.New(slog.DiscardHandler),
				Lookups: func(flowconfig.LookupOptions) flowconfig.LookupFunc {
					return func(context.Context, string) (string, bool, error) { return "acme", true, nil }
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			flow, err := graph.Compile("identity", fc, reg)
			if err != nil {
				t.Fatal(err)
			}
			nodes := map[string]graph.NodeConfig{}
			for _, nc := range fc.Nodes {
				nodes[nc.ID] = nc
			}

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, c.path, bytes.NewReader(c.body))
			req.Header.Set("Content-Type", c.contentType)
			req.Header.Set("X-Signature", "sig-123")
			head := flowconfig.HeadFromRequest(req, c.pathVars, "10.0.0.1")
			head.EventID, head.RequestID, head.TraceID = "evt-1", "req-1", "trace-1"
			ctx := flowconfig.WithBodyReader(context.Background(), bytes.NewReader(c.body))

			var mu sync.Mutex
			var events []graph.FailureEvent
			_, _ = flow.Run(ctx, head, graph.RunOptions{
				PublishTimeout: 5 * time.Second, FanoutConcurrency: 4,
				OnFailure: func(ev graph.FailureEvent) {
					mu.Lock()
					events = append(events, ev)
					mu.Unlock()
				},
			})
			if len(events) != len(c.failing) {
				t.Fatalf("OnFailure fired %d times, want %d (one per failing destination)", len(events), len(c.failing))
			}
			firstCalls := map[string]recordedPublish{}
			for _, d := range c.failing {
				firstCalls[d] = pub.lastCall(t, d)
			}
			pub.mu.Lock()
			pub.failing = map[string]bool{}
			pub.mu.Unlock()

			for _, ev := range events {
				if ev.NodeType != "publish" {
					t.Fatalf("failure at %s (%s), want only publish failures in these cases", ev.NodeID, ev.NodeType)
				}
				opts, err := flowconfig.PublishNodeOptions(nodes[ev.NodeID])
				if err != nil {
					t.Fatal(err)
				}
				input, ok := ev.Input.(graph.Envelope)
				if !ok {
					t.Fatalf("Input = %T, want graph.Envelope", ev.Input)
				}
				first := firstCalls[opts.Destination]
				original, err := publishmsg.FromEnvelope(first.env, opts.Destination, first.target)
				if err != nil {
					t.Fatal(err)
				}

				// Capture's rendering path rebuilds exactly what failed.
				target, prepared, err := flowconfig.PreparePublishForNode("", opts, input)
				if err != nil {
					t.Fatal(err)
				}
				rebuilt, err := publishmsg.FromEnvelope(prepared, opts.Destination, target)
				if err != nil {
					t.Fatal(err)
				}
				if target != first.target {
					t.Fatalf("%s: PreparePublish target %q, publish node sent %q", ev.NodeID, target, first.target)
				}
				assertSameMessage(t, ev.NodeID+" rebuilt", original, rebuilt)

				// The snapshot restores the exact input.
				dto, err := SnapshotFromEnvelope(input)
				if err != nil {
					t.Fatal(err)
				}
				data, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindResume, Envelope: &dto})
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := deliverysnapshot.Decode(data)
				if err != nil {
					t.Fatal(err)
				}
				restored, err := EnvelopeFromSnapshot(*decoded.Envelope)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(restored, input) {
					t.Fatalf("%s: snapshot round trip changed the input:\n got  %#v\n want %#v", ev.NodeID, restored, input)
				}

				// The redrive sends the same bytes.
				before := pub.callCount()
				if _, err := flow.RunFrom(context.Background(), ev.NodeID, restored, graph.RunOptions{PublishTimeout: 5 * time.Second}); err != nil {
					t.Fatalf("%s: RunFrom = %v", ev.NodeID, err)
				}
				// The redrive must publish exactly once, to the failed node's
				// destination: never nothing, and never a node upstream of
				// the resume point (the continuation case's archive).
				redrive := pub.callsSince(before)
				if len(redrive) != 1 || redrive[0].destination != opts.Destination {
					t.Fatalf("%s: redrive published %d time(s) %v, want once to %q", ev.NodeID, len(redrive), destinationsOf(redrive), opts.Destination)
				}
				second := redrive[0]
				redriven, err := publishmsg.FromEnvelope(second.env, opts.Destination, second.target)
				if err != nil {
					t.Fatal(err)
				}
				if second.target != first.target {
					t.Fatalf("%s: redrive target %q, original %q", ev.NodeID, second.target, first.target)
				}
				assertSameMessage(t, ev.NodeID+" redriven", original, redriven)
				wantPayload, err := RenderPayload(c.dests[opts.Destination], original)
				if err != nil {
					t.Fatal(err)
				}
				gotPayload, err := RenderPayload(c.dests[opts.Destination], redriven)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(wantPayload, gotPayload) {
					t.Fatalf("%s: payload differs:\n want %+v\n got  %+v", ev.NodeID, wantPayload, gotPayload)
				}
			}
		})
	}
}

func TestNativeCredentialProfileReferenceAffectsNodeIdentity(t *testing.T) {
	var first, second graph.NodeConfig
	if err := yaml.Unmarshal([]byte("id: up\ntype: http_request\nurl: https://api.example.org/sample\ncredential: {profile: sample_a}\n"), &first); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte("id: up\ntype: http_request\nurl: https://api.example.org/sample\ncredential: {profile: sample_b}\n"), &second); err != nil {
		t.Fatal(err)
	}
	if NodeConfigHash(first) == NodeConfigHash(second) {
		t.Fatal("different credential profile references share replay identity")
	}
}
