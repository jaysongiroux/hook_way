package flowconfig

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestAuthenticatedOriginalRepresentationSurvivesTransformsButNotRawChanges(t *testing.T) {
	ctx := context.Background()
	node := mustAuthNode(t, "scheme: hmac_sha256_headers_dot\nsecret: synthetic\nsignature_header: X-Sig\ntimestamp_header: X-Time\ntimestamp_unit: milliseconds\ntimestamp_policy: age\ntimestamp_tolerance: 5m\nbody_representation: json_js\n")
	raw := []byte(`{ "b":2,"1":1,"a":9007199254740993,"u":"\ud800" }`)
	want := `{"1":1,"b":2,"a":9007199254740992,"u":"\ud800"}`
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte("synthetic"))
	_, _ = mac.Write([]byte(ts + "." + want))
	input := graph.RawRequest{Body: raw, Head: graph.RequestHead{Header: http.Header{"X-Sig": {hex.EncodeToString(mac.Sum(nil))}, "X-Time": {ts}}}}
	out, err := node.Run(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	verified := out.(graph.Verified)
	if verified.OriginalJSON == nil {
		t.Fatal("successful auth did not hand off representation")
	}
	encoded, err := json.Marshal(verified)
	if err != nil || strings.Contains(string(encoded), "OriginalJSON") {
		t.Fatal("transient cache serialized", err)
	}
	env := graph.Envelope{V: verified, Body: map[string]any{"changed": true}, BodyFormat: "json"}
	id := PublishIdentityOptions{Source: "json_js_sha256"}
	_, prepared, err := PreparePublishForNode("", PublishOptions{Destination: "broker", Target: "synthetic.fifo", WireBody: "json_js", FIFO: &PublishFIFOOptions{GroupID: id, DeduplicationID: &id}}, env)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(want))
	if string(prepared.Publish.Body) != want || prepared.Publish.FIFO.DeduplicationID != hex.EncodeToString(hash[:]) {
		t.Fatal("auth/publication bytes diverged")
	}
	// A changed parsed body does not change the original-body wire contract.
	// A changed raw source must reject the cache and compute its own bytes.
	env.V.Raw.Body = []byte(` { "other":3 } `)
	_, changed, err := PreparePublishForNode("", PublishOptions{Destination: "broker", Target: "other", WireBody: "json_js"}, env)
	if err != nil || string(changed.Publish.Body) != `{"other":3}` {
		t.Fatal("stale raw bytes reused", err)
	}
	input.Head.Header.Set("X-Sig", strings.Repeat("0", 64))
	if payload, err := node.Run(ctx, input); err == nil || payload != nil {
		t.Fatal("failed auth released verified payload")
	}
}
