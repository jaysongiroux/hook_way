package deliverysnapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fullEnvelope() *Envelope {
	b64 := "eyJhIjoxfQ=="
	return &Envelope{
		Verified: Verified{
			Raw: RawRequest{
				Head: RequestHead{
					Method: "POST", Path: "/webhooks/orders",
					PathVars: map[string]string{"route": "orders"},
					Query:    map[string][]string{"q": {"1", "2"}},
					Header:   map[string][]string{"Content-Type": {"application/json"}},
					SourceIP: "10.0.0.1", Hops: 1, RequestID: "req", TraceID: "trace",
					Traceparent: "00-abc-def-01", EventID: "evt",
				},
				Body: []byte(`{"a":1}`),
			},
			Identity: "hmac",
		},
		Body: &Value{Type: TypeMap, Map: map[string]*Value{
			"n":     {Type: TypeInt64, Text: "1152921504606846976"},
			"f":     {Type: TypeFloat64, Text: "2.5"},
			"s":     {Type: TypeString, Text: "x"},
			"nil":   {Type: TypeNull},
			"empty": {Type: TypeMap},
			"list":  {Type: TypeList, List: []*Value{{Type: TypeBool, Bool: true}}},
			"xml": {Type: TypeXML, XML: &XMLDocument{
				Declaration: `<?xml version="1.0"?>`,
				Root: &XMLElement{
					Name:     xml.Name{Space: "urn:a", Local: "order"},
					Attr:     []xml.Attr{{Name: xml.Name{Local: "id"}, Value: "7"}},
					Children: []*XMLElement{{Name: xml.Name{Local: "item"}, Text: " 1 "}},
				},
			}},
		}},
		BodyFormat:        "json",
		Vars:              map[string]string{"route": "orders"},
		Upstream:          &Upstream{Values: map[string]string{"created_id": "sample-1"}, SelectionError: "missing_field", Status: 200, Header: map[string][]string{"X": {"y"}}, Body: []byte("ok")},
		CapturedEvent:     &CapturedEvent{Event: WebhookEnvelope{ID: "evt", ReceivedAt: time.Date(2026, 9, 23, 12, 0, 0, 5, time.UTC), RawBodyBase64: &b64, Auth: AuthInfo{Type: "hmac", Verified: true}, Query: &Value{Type: TypeNilMap}, Body: &Value{Type: TypeNull}}},
		Branch:            "",
		MessageAttributes: map[string]MessageAttribute{"tenant": {DataType: "String", Value: "acme"}},
		PayloadMode:       "envelope",
	}
}

func TestEncodeDecodeRoundTripsAResumeSnapshot(t *testing.T) {
	in := Snapshot{Kind: KindResume, Envelope: fullEnvelope()}
	data, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	in.Version = Version
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed the snapshot:\n in  %#v\n out %#v", in, out)
	}
}

func TestEncodeDecodeRoundTripsAPublishOnlySnapshot(t *testing.T) {
	in := Snapshot{Kind: KindPublishOnly, Message: &Message{
		SchemaVersion: "1.0", ID: "evt", Endpoint: "/w", DestinationQueueID: "hook", Queue: "orders",
		ReceivedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		Headers:    map[string][]string{"A": {"b"}},
		Query:      &Value{Type: TypeMap, Map: map[string]*Value{"q": {Type: TypeStrings, Strings: []string{"1"}}}},
		Body:       &Value{Type: TypeNull},
		Auth:       &AuthInfo{Type: "no_auth"},
		Variables:  map[string]string{"k": "v"},
		RawBody:    []byte("raw"), OrigHeaders: map[string][]string{"X-Sig": {"s"}}, Hops: 3,
		DeadLetter:        &DeadLetterMeta{Destination: "hook", Target: "orders", Outcome: "5xx", Attempts: 3, FailedAt: time.Date(2026, 9, 23, 12, 1, 0, 0, time.UTC)},
		MessageAttributes: map[string]MessageAttribute{"a": {DataType: "String", Value: "b"}},
		PayloadMode:       "object", ObjectBytes: []byte{0, 1, 2}, ObjectContentType: "application/octet-stream", ObjectFilename: "f.bin",
	}}
	data, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	in.Version = Version
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip changed the snapshot:\n in  %#v\n out %#v", in, out)
	}
}

func TestSnapshotRoundTripPreservesNilAndEmpty(t *testing.T) {
	in := Snapshot{Kind: KindResume, Envelope: &Envelope{
		Vars: map[string]string{},
		Verified: Verified{Raw: RawRequest{
			Head: RequestHead{PathVars: nil, Query: map[string][]string{}, Header: nil},
			Body: []byte{},
		}},
		MessageAttributes: nil,
		Body:              &Value{Type: TypeList, List: []*Value{}},
	}}
	data, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	e := out.Envelope
	if e.Vars == nil || e.Verified.Raw.Head.PathVars != nil || e.Verified.Raw.Head.Query == nil ||
		e.Verified.Raw.Head.Header != nil || e.Verified.Raw.Body == nil || e.MessageAttributes != nil {
		t.Fatalf("nil/empty distinctions lost: %#v", e)
	}
}

func TestEncodeSetsTheVersion(t *testing.T) {
	data, err := Encode(Snapshot{Version: 99, Kind: KindResume, Envelope: &Envelope{}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(data)
	if err != nil || out.Version != Version {
		t.Fatalf("Decode = %+v, %v; want version %d", out, err, Version)
	}
}

func gz(t *testing.T, raw string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeRejectsAnUnknownVersion(t *testing.T) {
	_, err := Decode(gz(t, `{"version":2,"kind":"resume","envelope":{}}`))
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Decode = %v, want ErrUnsupportedVersion", err)
	}
}

func TestDecodeRejectsMalformedInput(t *testing.T) {
	cases := map[string][]byte{
		"not gzip":                   []byte("plain"),
		"bad json":                   gz(t, `{`),
		"unknown kind":               gz(t, `{"version":1,"kind":"other","envelope":{}}`),
		"resume without envelope":    gz(t, `{"version":1,"kind":"resume"}`),
		"publish_only with envelope": gz(t, `{"version":1,"kind":"publish_only","envelope":{},"message":{}}`),
		"truncated":                  gz(t, `{"version":1,"kind":"resume","envelope":{}}`)[:10],
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(data); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Decode = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestDecodeRejectsOversizedInput(t *testing.T) {
	prev := maxDecodedBytes
	maxDecodedBytes = 64
	t.Cleanup(func() { maxDecodedBytes = prev })
	big := `{"version":1,"kind":"resume","envelope":{"body_format":"` + strings.Repeat("x", 200) + `"}}`
	if _, err := Decode(gz(t, big)); !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Decode = %v, want an ErrMalformed size error", err)
	}
}

// The replay service imports this package, so it must not pull in core
// packages or destination SDKs.
func TestPackageImportsOnlyTheStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if first := strings.SplitN(path, "/", 2)[0]; strings.Contains(first, ".") {
				t.Errorf("%s imports %s; only the standard library is allowed", name, path)
			}
		}
	}
}

// A newer build may change the snapshot's shape; the version must be read
// before the shape, so the error names the real cause.
func TestDecodeReportsAFutureVersionEvenWhenItsShapeDiffers(t *testing.T) {
	_, err := Decode(gz(t, `{"version":2,"kind":"resume","envelope":{"vars":["a"]}}`))
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Decode = %v, want ErrUnsupportedVersion", err)
	}
}

// A field this build does not know means the snapshot came from a build that
// forgot to bump the version; dropping it would redrive a different input.
func TestDecodeRejectsUnknownFields(t *testing.T) {
	for name, raw := range map[string]string{
		"top level": `{"version":1,"kind":"resume","envelope":{},"extra":1}`,
		"nested":    `{"version":1,"kind":"resume","envelope":{"future_field":1}}`,
		"in value":  `{"version":1,"kind":"resume","envelope":{"body":{"t":"null","z":1}}}`,
		"trailing":  `{"version":1,"kind":"resume","envelope":{}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(gz(t, raw)); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Decode = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestVersionOneIncludesPublishMetadata(t *testing.T) {
	snapshot, err := Decode(gz(t, `{"version":1,"kind":"resume","envelope":{"publish":{"destination":"broker"}}}`))
	if err != nil || snapshot.Version != Version || snapshot.Envelope.Publish.Destination != "broker" {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
}
