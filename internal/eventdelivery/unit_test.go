package eventdelivery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

func TestCanonicalIdentityAndExactFilters(t *testing.T) {
	for _, pair := range [][2]string{{`{"b":1.0,"a":"x"}`, `{"a":"x","b":1e0}`}, {`{"n":9007199254740993}`, `{"n":9007199254740993.00}`}} {
		var a, b map[string]any
		_ = flowconfig.DecodeMCPJSON([]byte(pair[0]), &a)
		_ = flowconfig.DecodeMCPJSON([]byte(pair[1]), &b)
		x, _ := canonical(a)
		y, _ := canonical(b)
		if string(x) != string(y) {
			t.Fatalf("canonical forms differ: %s %s", x, y)
		}
	}
	e := event{opts: flowconfig.MCPEventOptions{Filters: map[string]string{"n": "nested.id"}}}
	if !matches(e, map[string]any{"n": json.Number("9007199254740993")}, map[string]any{"nested": map[string]any{"id": json.Number("9007199254740993.0")}}) {
		t.Fatal("exact filter mismatch")
	}
	if matches(e, map[string]any{"n": json.Number("9007199254740993")}, map[string]any{"nested": map[string]any{"id": json.Number("9007199254740992")}}) {
		t.Fatal("rounded numbers matched")
	}
	if matches(e, map[string]any{"n": "1"}, map[string]any{"nested": map[string]any{"id": json.Number("1")}}) {
		t.Fatal("number coerced to string")
	}
}
func TestSignatureAndPrivateCallbackPolicy(t *testing.T) {
	secret := []byte(strings.Repeat("x", 32))
	body := []byte(`{"x":1}`)
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write([]byte("id.1."))
	_, _ = h.Write(body)
	want := "v1," + base64.StdEncoding.EncodeToString(h.Sum(nil))
	if signature(secret, "id", "1", body) != want {
		t.Fatal("invalid signature")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("private target reached"); w.WriteHeader(200) }))
	defer server.Close()
	r := &Runtime{client: flowconfig.NewCredentialHTTPClient(true)}
	defer r.client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := r.send(ctx, server.URL, "id", "sub", body, secret, nil)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("private callback accepted")
	}
	for _, u := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/#x", "file:///tmp/a"} {
		if validCallback(u) {
			t.Fatalf("accepted %s", u)
		}
	}
}

func TestCanonicalNestedNumbersAndTypeIdentity(t *testing.T) {
	equivalent := [][2]string{{`[-0,1e2,true,null,{"a":1,"b":2}]`, `[0,100.0,true,null,{"b":2.0,"a":1.0}]`}, {`{"x":0.00001}`, `{"x":1e-5}`}}
	for _, pair := range equivalent {
		var a, b any
		_ = flowconfig.DecodeMCPJSON([]byte(pair[0]), &a)
		_ = flowconfig.DecodeMCPJSON([]byte(pair[1]), &b)
		x, e := canonical(a)
		y, f := canonical(b)
		if e != nil || f != nil || string(x) != string(y) {
			t.Fatal("semantic number identity changed", e, f, string(x), string(y))
		}
	}
	if _, err := canonical(json.Number("1e99999999999999999999")); err == nil {
		t.Fatal("unbounded exponent accepted")
	}
	if r, err := New(context.Background(), flowconfig.Config{}, nil); err != nil || r != nil {
		t.Fatal("disabled runtime opened")
	}
	if _, err := Open(context.Background(), flowconfig.EventDeliveryConfig{}); err == nil {
		t.Fatal("invalid store opened")
	}
}
